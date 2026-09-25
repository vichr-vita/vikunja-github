package worker

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"
	"vikunja-github/internal/github"
	"vikunja-github/internal/model"
	"vikunja-github/internal/store"
	"vikunja-github/internal/vikunja"
)

type Processor interface {
	Process(context.Context, model.Event, int64) error
}
type Worker struct {
	Store                      *store.Store
	Processor                  Processor
	MaxAttempts, RetentionDays int
	Log                        *slog.Logger
	Ready                      atomic.Bool
}

func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 10 {
		attempt = 10
	}
	return min(time.Second*time.Duration(1<<uint(attempt-1)), 5*time.Minute)
}

// Run owns processing for this database. Storage bookkeeping failures stop the
// worker and take readiness down rather than silently stranding a claimed event.
func (w *Worker) Run(ctx context.Context) error {
	if e := w.Store.Recover(ctx); e != nil {
		return e
	}
	w.Ready.Store(true)
	defer w.Ready.Store(false)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	if e := w.Store.Cleanup(ctx, time.Now().Add(-time.Duration(w.RetentionDays)*24*time.Hour)); e != nil {
		return e
	}
	for {
		processed, e := w.Step(ctx)
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		case <-cleanup.C:
			if e := w.Store.Cleanup(ctx, time.Now().Add(-time.Duration(w.RetentionDays)*24*time.Hour)); e != nil {
				return e
			}
		}
	}
}
func (w *Worker) Step(ctx context.Context) (bool, error) {
	d, e := w.Store.Claim(ctx, time.Now())
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	log := w.Log.With("delivery_id", d.ID, "github_event", d.EventType, "github_action", d.Action, "attempt", d.Attempts)
	event, e := github.Normalize(d.EventType, d.Payload)
	retryable := false
	if e == nil {
		e = w.Processor.Process(ctx, event, d.Sequence)
		retryable = vikunja.Retryable(e)
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	retry := e != nil && retryable && d.Attempts < w.MaxAttempts
	if finishErr := w.Store.Finish(ctx, d, e, retry, Backoff(d.Attempts)); finishErr != nil {
		return true, finishErr
	}
	if e == nil {
		log.Info("delivery_processed")
	} else if retry {
		log.Warn("delivery_retry_scheduled", "error", e, "retry_in", Backoff(d.Attempts))
	} else {
		log.Error("delivery_failed", "error", e)
	}
	return true, nil
}
