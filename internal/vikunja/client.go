// Package vikunja implements the inspected Vikunja v2.6.0 API contract.
package vikunja

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Task struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}
type Comment struct {
	ID      int64  `json:"id"`
	Comment string `json:"comment"`
}
type Client struct {
	base, token string
	http        *http.Client
}
type APIError struct{ Status int }

func (e *APIError) Error() string { return fmt.Sprintf("Vikunja returned HTTP %d", e.Status) }
func IsStatus(err error, status int) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == status
}
func Retryable(err error) bool {
	var e *APIError
	if errors.As(err, &e) {
		return e.Status == 429 || e.Status >= 500 || e.Status == 408
	}
	return true
}
func New(base, token string) *Client {
	return &Client{strings.TrimRight(base, "/") + "/api/v2", token, &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/merge-patch+json")
		req.Header.Set("X-Vikunja-Format", "markdown")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Do not persist URLs or arbitrary server error bodies in delivery diagnostics.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("Vikunja network request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{resp.StatusCode}
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("invalid Vikunja JSON response: %w", err)
	}
	return nil
}
func (c *Client) ResolveTask(ctx context.Context, project string, index int64) (Task, error) {
	var t Task
	e := c.request(ctx, "GET", "/projects/"+url.PathEscape(project)+"/tasks/by-index/"+strconv.FormatInt(index, 10), nil, &t)
	if e == nil && t.ID <= 0 {
		e = fmt.Errorf("Vikunja returned invalid task ID")
	}
	return t, e
}
func (c *Client) GetTask(ctx context.Context, id int64) (Task, error) {
	var t Task
	e := c.request(ctx, "GET", fmt.Sprintf("/tasks/%d", id), nil, &t)
	return t, e
}
func (c *Client) CreateComment(ctx context.Context, task int64, markdown string) (Comment, error) {
	var v Comment
	e := c.request(ctx, "POST", fmt.Sprintf("/tasks/%d/comments?format=markdown", task), map[string]string{"comment": markdown}, &v)
	if e == nil && v.ID <= 0 {
		e = fmt.Errorf("Vikunja returned invalid comment ID")
	}
	return v, e
}
func (c *Client) UpdateComment(ctx context.Context, task, id int64, markdown string) (Comment, error) {
	var v Comment
	e := c.request(ctx, "PATCH", fmt.Sprintf("/tasks/%d/comments/%d", task, id), map[string]string{"comment": markdown}, &v)
	if e == nil && v.ID <= 0 {
		e = fmt.Errorf("Vikunja returned invalid comment ID")
	}
	return v, e
}

// FindComment scans every page. The visible marker survives HTML sanitization and
// Markdown round trips, unlike an HTML comment or custom HTML attribute.
func (c *Client) FindComment(ctx context.Context, task int64, marker string) (Comment, error) {
	for page := 1; ; page++ {
		var v struct {
			Items      []Comment `json:"items"`
			TotalPages int       `json:"total_pages"`
		}
		e := c.request(ctx, "GET", fmt.Sprintf("/tasks/%d/comments?format=markdown&page=%d&per_page=100", task, page), nil, &v)
		if e != nil {
			return Comment{}, e
		}
		for _, x := range v.Items {
			if strings.Contains(x.Comment, marker) {
				return x, nil
			}
		}
		if page >= v.TotalPages {
			return Comment{}, nil
		}
	}
}
