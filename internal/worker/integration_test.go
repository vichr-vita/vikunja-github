package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"vikunja-github/internal/github"
	"vikunja-github/internal/processor"
	"vikunja-github/internal/store"
	"vikunja-github/internal/taskref"
	"vikunja-github/internal/vikunja"
)

func TestWebhookToCommentAcrossOutageAndRestart(t *testing.T) {
	var offline atomic.Bool
	offline.Store(true)
	var creates atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			w.WriteHeader(503)
			return
		}
		switch {
		case r.URL.Path == "/api/v2/projects/LEDGER/tasks/by-index/42":
			fmt.Fprint(w, `{"id":1387}`)
		case r.Method == "GET":
			fmt.Fprint(w, `{"items":[],"total_pages":0}`)
		case r.Method == "POST":
			creates.Add(1)
			fmt.Fprint(w, `{"id":73}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer api.Close()
	path := filepath.Join(t.TempDir(), "db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	body := []byte(`{"ref_type":"branch","ref":"LEDGER-42","repository":{"id":1,"full_name":"owner/repo","html_url":"https://github.com/owner/repo"}}`)
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader(body))
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-GitHub-Delivery", "one")
	req.Header.Set("X-GitHub-Event", "create")
	out := httptest.NewRecorder()
	github.Handler{Store: s, Secret: "secret", Log: log}.ServeHTTP(out, req)
	if out.Code != 202 {
		t.Fatal(out.Code)
	}
	matcher, _ := taskref.New("")
	makeWorker := func(s *store.Store) *Worker {
		return &Worker{Store: s, Processor: &processor.Processor{Store: s, API: vikunja.New(api.URL, "token"), Matcher: matcher, MaxCommits: 10, Log: log}, MaxAttempts: 10, Log: log}
	}
	w := makeWorker(s)
	if _, e = w.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	var status string
	s.DB.QueryRow(`SELECT status FROM webhook_deliveries`).Scan(&status)
	if status != "pending" || creates.Load() != 0 {
		t.Fatal(status)
	}
	s.Close()
	s, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	offline.Store(false)
	s.DB.Exec(`UPDATE webhook_deliveries SET next_attempt_at=0`)
	w = makeWorker(s)
	if e = s.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = w.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	s.DB.QueryRow(`SELECT status FROM webhook_deliveries`).Scan(&status)
	if status != "processed" || creates.Load() != 1 {
		t.Fatal(status, creates.Load())
	}
	var task, comment int64
	if e = s.DB.QueryRow(`SELECT task_id,comment_id FROM managed_comments`).Scan(&task, &comment); e != nil || task != 1387 || comment != 73 {
		t.Fatal(task, comment, e)
	}
}
