package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"vikunja-github/internal/store"
)

func VerifySignature(secret string, body []byte, signature string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	got, e := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if e != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), got)
}

type DeliveryStore interface {
	Ingest(context.Context, store.Delivery) (bool, error)
}
type Handler struct {
	Store        DeliveryStore
	Secret       string
	Repositories map[string]bool
	Log          *slog.Logger
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 25<<20))
	if e != nil {
		http.Error(w, "payload too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	if !VerifySignature(h.Secret, body, r.Header.Get("X-Hub-Signature-256")) {
		h.Log.Warn("signature_invalid")
		http.Error(w, "invalid signature", 401)
		return
	}
	id := r.Header.Get("X-GitHub-Delivery")
	event := r.Header.Get("X-GitHub-Event")
	if id == "" || len(id) > 200 || event == "" || len(event) > 100 {
		http.Error(w, "missing or invalid delivery/event header", 400)
		return
	}
	normalized, e := Normalize(event, body)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if len(h.Repositories) > 0 && !h.Repositories[strings.ToLower(normalized.Repository.FullName)] {
		w.WriteHeader(200)
		return
	}
	inserted, e := h.Store.Ingest(r.Context(), store.Delivery{ID: id, EventType: event, Action: normalized.Action, Payload: body})
	if e != nil {
		h.Log.Error("webhook_persist_failed", "delivery_id", id)
		http.Error(w, "storage unavailable", 503)
		return
	}
	if !inserted {
		h.Log.Info("webhook_duplicate", "delivery_id", id)
		w.WriteHeader(200)
		return
	}
	h.Log.Info("webhook_received", "delivery_id", id, "github_event", event, "github_action", normalized.Action, "repository", normalized.Repository.FullName)
	w.WriteHeader(202)
}
