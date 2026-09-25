package vikunja

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientContract(t *testing.T) {
	pages := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/api/v2/projects/LEDGER/tasks/by-index/42", "/api/v2/tasks/1387":
			fmt.Fprint(w, `{"id":1387,"title":"Task"}`)
		case "/api/v2/tasks/1387/comments":
			if r.URL.Query().Get("format") != "markdown" {
				t.Error("format")
			}
			if r.Method == "POST" {
				var b map[string]string
				json.NewDecoder(r.Body).Decode(&b)
				if b["comment"] != "hello" {
					t.Error(b)
				}
				w.WriteHeader(201)
				fmt.Fprint(w, `{"id":5,"comment":"hello"}`)
			} else {
				pages++
				if r.URL.Query().Get("page") == "1" {
					fmt.Fprint(w, `{"items":[{"id":3,"comment":"other"}],"total_pages":2}`)
				} else {
					fmt.Fprint(w, `{"items":[{"id":5,"comment":"marker"}],"total_pages":2}`)
				}
			}
		case "/api/v2/tasks/1387/comments/5":
			if r.Method != "PATCH" || r.Header.Get("Content-Type") != "application/merge-patch+json" || r.Header.Get("X-Vikunja-Format") != "markdown" {
				t.Error("patch headers")
			}
			fmt.Fprint(w, `{"id":5}`)
		default:
			t.Error(r.URL)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	c := New(s.URL, "token")
	ctx := context.Background()
	task, e := c.ResolveTask(ctx, "LEDGER", 42)
	if e != nil || task.ID != 1387 {
		t.Fatal(task, e)
	}
	if _, e = c.GetTask(ctx, 1387); e != nil {
		t.Fatal(e)
	}
	if _, e = c.CreateComment(ctx, 1387, "hello"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.UpdateComment(ctx, 1387, 5, "hello"); e != nil {
		t.Fatal(e)
	}
	found, e := c.FindComment(ctx, 1387, "marker")
	if e != nil || found.ID != 5 || pages != 2 {
		t.Fatal(found, e, pages)
	}
}
func TestFailures(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, "sensitive response")
			}))
			defer s.Close()
			_, e := New(s.URL, "token").ResolveTask(context.Background(), "X", 1)
			if !IsStatus(e, status) {
				t.Fatal(e)
			}
			if Retryable(e) != (status == 429 || status == 500) {
				t.Fatal(e)
			}
		})
	}
	s := httptest.NewServer(http.NotFoundHandler())
	s.Close()
	_, e := New(s.URL, "token").ResolveTask(context.Background(), "X", 1)
	if e == nil || !Retryable(e) {
		t.Fatal(e)
	}
}
