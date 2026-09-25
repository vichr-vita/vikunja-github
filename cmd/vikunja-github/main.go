package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"vikunja-github/internal/config"
	"vikunja-github/internal/github"
	"vikunja-github/internal/processor"
	"vikunja-github/internal/store"
	"vikunja-github/internal/taskref"
	"vikunja-github/internal/vikunja"
	"vikunja-github/internal/worker"
)

func main() {
	if e := run(); e != nil {
		slog.Error("service_stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	c, e := config.Load()
	if e != nil {
		return e
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: c.LogLevel}))
	slog.SetDefault(log)
	if e = os.MkdirAll(filepath.Dir(c.DatabasePath), 0700); e != nil {
		return e
	}
	db, e := store.Open(c.DatabasePath)
	if e != nil {
		return e
	}
	defer db.Close()
	matcher, e := taskref.New(c.Pattern)
	if e != nil {
		return e
	}
	p := &processor.Processor{Store: db, API: vikunja.New(c.BaseURL, c.Token), Matcher: matcher, Projects: c.Projects, MaxCommits: c.MaxCommits, Log: log}
	w := &worker.Worker{Store: db, Processor: p, MaxAttempts: c.MaxAttempts, RetentionDays: c.RetentionDays, Log: log}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	mux := http.NewServeMux()
	mux.Handle("POST /webhooks/github", github.Handler{Store: db, Secret: c.Secret, Repositories: c.Repositories, Log: log})
	mux.HandleFunc("GET /healthz", func(out http.ResponseWriter, _ *http.Request) { fmt.Fprintln(out, "ok") })
	mux.HandleFunc("GET /readyz", func(out http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if !w.Ready.Load() || db.DB.PingContext(ctx) != nil {
			http.Error(out, "not ready", 503)
			return
		}
		fmt.Fprintln(out, "ok")
	})
	server := &http.Server{Addr: c.ListenAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	workerDone := make(chan error, 1)
	go func() { workerDone <- w.Run(ctx) }()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ListenAndServe() }()
	log.Info("service_started", "listen_addr", c.ListenAddr)
	var result error
	workerExited := false
	select {
	case <-ctx.Done():
	case result = <-workerDone:
		workerExited = true
	case result = <-serverDone:
		if errors.Is(result, http.ErrServerClosed) {
			result = nil
		}
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	if !workerExited {
		select {
		case err := <-workerDone:
			if result == nil {
				result = err
			}
		case <-shutdown.Done():
			return shutdown.Err()
		}
	}
	return result
}
