package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"vikunja-github/internal/store"
)

func sign(body []byte) string {
	m := hmac.New(sha256.New, []byte("secret"))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}
func TestSignature(t *testing.T) {
	body := []byte("Hello, World!")
	known := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if !VerifySignature("It's a Secret to Everybody", body, known) {
		t.Fatal("GitHub published fixture failed")
	}
	for _, signature := range []string{"", "sha256=bad", "sha1=" + known, known} {
		if VerifySignature("wrong", body, signature) {
			t.Fatal("invalid accepted")
		}
	}
	if VerifySignature("It's a Secret to Everybody", []byte("modified"), known) {
		t.Fatal("modified body accepted")
	}
}
func TestWebhookDurabilityAndDuplicates(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	h := Handler{Store: db, Secret: "secret", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	body := []byte(`{"repository":{"id":1,"full_name":"a/b"},"ref_type":"branch","ref":"feat/LEDGER-42"}`)
	send := func(id, event string, b []byte, sig string) int {
		r := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader(b))
		r.Header.Set("X-GitHub-Delivery", id)
		r.Header.Set("X-GitHub-Event", event)
		r.Header.Set("X-Hub-Signature-256", sig)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := send("one", "create", body, sign(body)); got != 202 {
		t.Fatal(got)
	}
	if got := send("one", "create", body, sign(body)); got != 200 {
		t.Fatal(got)
	}
	for _, tt := range []struct {
		id   string
		b    []byte
		sig  string
		want int
	}{{"x", body, "", 401}, {"x", body, "sha256=abc", 401}, {"x", []byte(`{`), sign([]byte(`{`)), 400}, {"", body, sign(body), 400}, {"x", []byte(`null`), sign([]byte(`null`)), 400}} {
		if got := send(tt.id, "create", tt.b, tt.sig); got != tt.want {
			t.Fatalf("got %d want %d", got, tt.want)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code := send("concurrent", "create", body, sign(body)); code != 200 && code != 202 {
				t.Errorf("code %d", code)
			}
		}()
	}
	wg.Wait()
	var n int
	if e = db.DB.QueryRow(`SELECT count(*) FROM webhook_deliveries`).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	d, e := db.Claim(context.Background(), time.Now())
	if e != nil || !bytes.Equal(d.Payload, body) {
		t.Fatal(d, e)
	}
	h.Repositories = map[string]bool{"other/repo": true}
	if got := send("ignored", "create", body, sign(body)); got != 200 {
		t.Fatal(got)
	}
}
func TestNormalize(t *testing.T) {
	for _, tt := range []struct {
		event, body, state string
		commits            int
	}{
		{"create", `{"ref_type":"branch","ref":"feat/LEDGER-42"}`, "active", 0}, {"delete", `{"ref_type":"branch","ref":"feat/LEDGER-42"}`, "deleted", 0}, {"create", `{"ref_type":"tag","ref":"v1"}`, "", 0}, {"delete", `{"ref_type":"tag","ref":"v1"}`, "", 0}, {"push", `{"ref":"refs/heads/feat/LEDGER-42","commits":[{"id":"abc","message":"one"}]}`, "active", 1}, {"push", `{"ref":"refs/heads/main","commits":[{"id":"abc"},{"id":"def"}]}`, "active", 2}, {"push", `{"ref":"refs/heads/main","deleted":true}`, "deleted", 0},
	} {
		t.Run(tt.event+tt.body, func(t *testing.T) {
			body := fmt.Sprintf(`{"repository":{"id":1,"full_name":"a/b"},%s`, tt.body[1:])
			e, err := Normalize(tt.event, []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			state := ""
			if e.Branch != nil {
				state = e.Branch.State
			}
			if state != tt.state || len(e.Commits) != tt.commits {
				t.Fatal(e)
			}
		})
	}
	for _, action := range []string{"opened", "synchronize", "closed"} {
		for _, merged := range []bool{false, true} {
			state := "open"
			if action == "closed" {
				state = "closed"
			}
			body := fmt.Sprintf(`{"repository":{"id":1,"full_name":"a/b"},"action":%q,"pull_request":{"number":7,"state":%q,"merged":%t,"head":{"ref":"feat/A-1","repo":{"id":1}}}}`, action, state, merged)
			e, err := Normalize("pull_request", []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if merged {
				state = "merged"
			}
			if e.PullRequest.State != state {
				t.Fatal(e)
			}
		}
	}
}
