package processor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"vikunja-github/internal/model"
	"vikunja-github/internal/store"
	"vikunja-github/internal/taskref"
	"vikunja-github/internal/vikunja"
)

type fakeAPI struct {
	tasks                      map[int64]int64
	comments                   map[int64]map[int64]string
	resolves, creates, updates int
	fail                       error
	loseResponse               bool
}

func (f *fakeAPI) ResolveTask(_ context.Context, _ string, n int64) (vikunja.Task, error) {
	f.resolves++
	if f.fail != nil {
		return vikunja.Task{}, f.fail
	}
	id := f.tasks[n]
	if id == 0 {
		return vikunja.Task{}, &vikunja.APIError{Status: 404}
	}
	return vikunja.Task{ID: id}, nil
}
func (f *fakeAPI) CreateComment(_ context.Context, task int64, s string) (vikunja.Comment, error) {
	if f.fail != nil {
		return vikunja.Comment{}, f.fail
	}
	f.creates++
	id := int64(f.creates)
	if f.comments[task] == nil {
		f.comments[task] = map[int64]string{}
	}
	f.comments[task][id] = s
	if f.loseResponse {
		f.loseResponse = false
		return vikunja.Comment{}, errors.New("connection dropped after POST")
	}
	return vikunja.Comment{ID: id, Comment: s}, nil
}
func (f *fakeAPI) UpdateComment(_ context.Context, task, id int64, s string) (vikunja.Comment, error) {
	if f.fail != nil {
		return vikunja.Comment{}, f.fail
	}
	f.updates++
	f.comments[task][id] = s
	return vikunja.Comment{ID: id, Comment: s}, nil
}
func (f *fakeAPI) FindComment(_ context.Context, task int64, marker string) (vikunja.Comment, error) {
	if f.fail != nil {
		return vikunja.Comment{}, f.fail
	}
	for id, s := range f.comments[task] {
		if strings.Contains(s, marker) {
			return vikunja.Comment{ID: id, Comment: s}, nil
		}
	}
	return vikunja.Comment{}, nil
}
func setup(t *testing.T) (*Processor, *fakeAPI) {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	f := &fakeAPI{tasks: map[int64]int64{42: 1387, 51: 1500}, comments: map[int64]map[int64]string{}}
	m, _ := taskref.New("")
	return &Processor{Store: db, API: f, Matcher: m, MaxCommits: 2, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, f
}

var repo = model.Repository{ID: 1, FullName: "owner/repo", URL: "https://github.com/owner/repo"}

func branch(name string) *model.BranchEvent {
	return &model.BranchEvent{Repository: repo, Name: name, State: "active"}
}
func run(t *testing.T, p *Processor, e model.Event, seq int64) {
	t.Helper()
	if err := p.Process(context.Background(), e, seq); err != nil {
		t.Fatal(err)
	}
}
func count(t *testing.T, p *Processor, query string, want int) {
	t.Helper()
	var n int
	if e := p.Store.DB.QueryRow(query).Scan(&n); e != nil || n != want {
		t.Fatalf("%s: got %d want %d (%v)", query, n, want, e)
	}
}
func TestAssociationsAndComments(t *testing.T) {
	p, f := setup(t)
	b := branch("feat/LEDGER-42-reconcile")
	event := model.Event{Repository: repo, Branch: b}
	run(t, p, event, 1)
	if f.creates != 1 {
		t.Fatal(f.creates)
	}
	run(t, p, event, 1)
	if f.creates != 1 || f.updates != 0 {
		t.Fatal("duplicate changed comment")
	}
	// A project move changes human reference resolution, not existing branch identity.
	f.tasks[42] = 9999
	event.Commits = []model.CommitEvent{{Repository: repo, Branch: b.Name, SHA: "aaaaaaaa", Message: "Implement matcher", OccurredAt: time.Unix(1, 0)}, {Repository: repo, Branch: b.Name, SHA: "bbbbbbbb", Message: "LEDGER-51 normalize", OccurredAt: time.Unix(2, 0)}}
	run(t, p, event, 2)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='commit' AND task_id=1387`, 2)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='commit' AND task_id=1500`, 1)
	count(t, p, `SELECT count(*) FROM github_references WHERE task_id=9999`, 0)
	// Explicit cross-task commits use a repository-level comment.
	count(t, p, `SELECT count(*) FROM managed_comments WHERE task_id=1500 AND branch_name=''`, 1)
	pr := &model.PullRequestEvent{Repository: repo, HeadRepositoryID: 1, Number: 73, HeadBranch: b.Name, Title: "LEDGER-51 reconcile", State: "open", UpdatedAt: time.Unix(10, 0)}
	run(t, p, model.Event{PullRequest: pr}, 3)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='pull_request'`, 2)
	pr.State = "merged"
	pr.UpdatedAt = time.Unix(20, 0)
	run(t, p, model.Event{PullRequest: pr}, 4)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='pull_request' AND state='merged'`, 2)
	for _, s := range f.comments[1387] {
		if !strings.Contains(s, "merged") {
			t.Fatal(s)
		}
	}
	// An older redelivery cannot reopen a merged PR.
	pr.State = "open"
	pr.UpdatedAt = time.Unix(10, 0)
	run(t, p, model.Event{PullRequest: pr}, 3)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='pull_request' AND state='merged'`, 2)
	b.State = "deleted"
	run(t, p, model.Event{Branch: b}, 5)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='branch' AND state='deleted'`, 1)
	for _, s := range f.comments[1387] {
		if !strings.Contains(s, "Status: deleted") {
			t.Fatal(s)
		}
	}
	b.State = "active"
	run(t, p, model.Event{Branch: b}, 2)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='branch' AND state='deleted'`, 1)
}
func TestMultipleMissingAndAllowedReferences(t *testing.T) {
	p, _ := setup(t)
	run(t, p, model.Event{Branch: branch("feat/LEDGER-404-LEDGER-42-LEDGER-51")}, 1)
	count(t, p, `SELECT count(*) FROM github_references`, 2)
	p.Projects = map[string]bool{"HOME": true}
	run(t, p, model.Event{Branch: branch("feat/LEDGER-42-other")}, 2)
	count(t, p, `SELECT count(*) FROM github_references`, 2)
}
func TestUnknownBranchCommitFallback(t *testing.T) {
	p, _ := setup(t)
	run(t, p, model.Event{Branch: branch("main"), Commits: []model.CommitEvent{{Repository: repo, Branch: "main", SHA: "abcdef", Message: "LEDGER-42 fix"}}}, 1)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='commit'`, 1)
	count(t, p, `SELECT count(*) FROM managed_comments WHERE branch_name=''`, 1)
}
func TestCommentCrashReconciliation(t *testing.T) {
	p, f := setup(t)
	f.loseResponse = true
	e := model.Event{Branch: branch("LEDGER-42")}
	if err := p.Process(context.Background(), e, 1); err == nil {
		t.Fatal("expected uncertain POST")
	}
	count(t, p, `SELECT count(*) FROM managed_comments WHERE comment_id=0`, 1)
	run(t, p, e, 1)
	if f.creates != 1 || len(f.comments[1387]) != 1 {
		t.Fatal("duplicate comment", f.creates)
	}
	count(t, p, `SELECT count(*) FROM managed_comments WHERE comment_id>0`, 1)
}
func TestBoundedCommits(t *testing.T) {
	p, f := setup(t)
	e := model.Event{Branch: branch("LEDGER-42")}
	for i := 0; i < 5; i++ {
		e.Commits = append(e.Commits, model.CommitEvent{Repository: repo, Branch: "LEDGER-42", SHA: fmt.Sprintf("%08d", i), Message: fmt.Sprintf("Commit %d", i), OccurredAt: time.Unix(int64(i), 0)})
	}
	run(t, p, e, 1)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='commit'`, 5)
	for _, s := range f.comments[1387] {
		if !strings.Contains(s, "5 commits total. Showing the latest 2.") || strings.Contains(s, "Commit 0") || !strings.Contains(s, "Commit 4") {
			t.Fatal(s)
		}
	}
}
func TestForkDoesNotInheritBaseBranch(t *testing.T) {
	p, _ := setup(t)
	run(t, p, model.Event{Branch: branch("work")}, 1) // Manually add an unrelated canonical association on the base branch.
	p.Store.Upsert(context.Background(), model.Reference{TaskID: 1387, Repository: repo, Kind: "branch", ExternalKey: "work", BranchName: "work"}, "work")
	run(t, p, model.Event{PullRequest: &model.PullRequestEvent{Repository: repo, HeadRepositoryID: 2, Number: 9, HeadBranch: "work", State: "open"}}, 2)
	count(t, p, `SELECT count(*) FROM github_references WHERE kind='pull_request'`, 0)
}
func TestAuthFailureSurfaced(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500} {
		p, f := setup(t)
		f.fail = &vikunja.APIError{Status: status}
		e := p.Process(context.Background(), model.Event{Branch: branch("LEDGER-42")}, 1)
		if !vikunja.IsStatus(e, status) {
			t.Fatal(e)
		}
	}
}

func TestPRTitleTaskCommentTracksBranchDeletion(t *testing.T) {
	p, f := setup(t)
	b := branch("LEDGER-42")
	run(t, p, model.Event{Branch: b}, 1)
	run(t, p, model.Event{PullRequest: &model.PullRequestEvent{Repository: repo, HeadRepositoryID: 1, Number: 1, HeadBranch: b.Name, Title: "LEDGER-51 cross task", State: "open"}}, 2)
	b.State = "deleted"
	run(t, p, model.Event{Branch: b}, 3)
	for _, s := range f.comments[1500] {
		if !strings.Contains(s, "Status: deleted") {
			t.Fatal(s)
		}
	}
}

func TestResolutionSurvivesPartialFailure(t *testing.T) {
	p, f := setup(t)
	ctx := context.Background()
	if e := p.Store.SaveResolution(ctx, repo.ID, "branch", "LEDGER-42", "LEDGER-42", 1387); e != nil {
		t.Fatal(e)
	}
	f.tasks[42] = 9999
	run(t, p, model.Event{Branch: branch("LEDGER-42")}, 1)
	count(t, p, `SELECT count(*) FROM github_references WHERE task_id=1387`, 1)
	if f.resolves != 0 {
		t.Fatal("re-resolved after crash")
	}
}
