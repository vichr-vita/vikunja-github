package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
	"vikunja-github/internal/model"
	"vikunja-github/internal/store"
	"vikunja-github/internal/vikunja"
)

type processorStub struct {
	err   error
	calls int
}

func (p *processorStub) Process(context.Context, model.Event, int64) error { p.calls++; return p.err }
func TestRestartAndRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s.Ingest(ctx, store.Delivery{ID: "one", EventType: "ping", Payload: []byte(`{}`)})
	s.Claim(ctx, time.Now())
	s.Close()
	s, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	p := &processorStub{err: errors.New("offline")}
	w := &Worker{Store: s, Processor: p, MaxAttempts: 4, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, e = w.Step(ctx); e != nil {
		t.Fatal(e)
	}
	var status string
	var attempts int
	s.DB.QueryRow(`SELECT status,attempts FROM webhook_deliveries`).Scan(&status, &attempts)
	if status != "pending" || attempts != 2 {
		t.Fatal(status, attempts)
	}
	s.DB.Exec(`UPDATE webhook_deliveries SET next_attempt_at=0`)
	p.err = nil
	if _, e = w.Step(ctx); e != nil {
		t.Fatal(e)
	}
	s.DB.QueryRow(`SELECT status FROM webhook_deliveries`).Scan(&status)
	if status != "processed" {
		t.Fatal(status)
	}
	if e = s.Cleanup(ctx, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	var size int
	s.DB.QueryRow(`SELECT length(payload) FROM webhook_deliveries`).Scan(&size)
	if size != 0 {
		t.Fatal(size)
	}
	inserted, e := s.Ingest(ctx, store.Delivery{ID: "one", EventType: "ping", Payload: []byte(`{}`)})
	if inserted || e != nil {
		t.Fatal(inserted, e)
	}
}
func TestFailureClassificationAndLimit(t *testing.T) {
	for _, tt := range []struct {
		name     string
		err      error
		attempts int
	}{{"unauthorized", &vikunja.APIError{Status: 401}, 1}, {"forbidden", &vikunja.APIError{Status: 403}, 1}, {"rate", &vikunja.APIError{Status: 429}, 3}, {"server", &vikunja.APIError{Status: 500}, 3}, {"network", errors.New("offline"), 3}} {
		t.Run(tt.name, func(t *testing.T) {
			s, e := store.Open(filepath.Join(t.TempDir(), "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			s.Ingest(context.Background(), store.Delivery{ID: "x", EventType: "ping", Payload: []byte(`{}`)})
			p := &processorStub{err: tt.err}
			w := Worker{Store: s, Processor: p, MaxAttempts: 3, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			for i := 0; i < tt.attempts; i++ {
				s.DB.Exec(`UPDATE webhook_deliveries SET next_attempt_at=0`)
				if _, e = w.Step(context.Background()); e != nil {
					t.Fatal(e)
				}
			}
			var status string
			s.DB.QueryRow(`SELECT status FROM webhook_deliveries`).Scan(&status)
			if status != "failed" || p.calls != tt.attempts {
				t.Fatal(status, p.calls)
			}
		})
	}
	if Backoff(100) > 5*time.Minute {
		t.Fatal("unbounded backoff")
	}
}
