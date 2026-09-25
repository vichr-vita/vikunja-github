package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
	"vikunja-github/internal/model"
)

//go:embed migrations/001_initial.sql
var schema string

type Store struct{ DB *sql.DB }
type Delivery struct {
	ID, EventType, Action string
	Payload               []byte
	Attempts              int
	Sequence              int64
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Ingest(ctx context.Context, d Delivery) (bool, error) {
	r, e := s.DB.ExecContext(ctx, `INSERT INTO webhook_deliveries(delivery_id,event_type,action,payload,status,received_at) VALUES(?,?,?,?,'pending',?) ON CONFLICT(delivery_id) DO NOTHING`, d.ID, d.EventType, d.Action, d.Payload, time.Now().UnixMilli())
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}

// Recover is called once at startup by the sole worker. One process owns a database.
func (s *Store) Recover(ctx context.Context) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE webhook_deliveries SET status='pending' WHERE status='processing'`)
	return e
}
func (s *Store) Claim(ctx context.Context, now time.Time) (Delivery, error) {
	var d Delivery
	e := s.DB.QueryRowContext(ctx, `UPDATE webhook_deliveries SET status='processing',attempts=attempts+1 WHERE sequence=(SELECT sequence FROM webhook_deliveries WHERE status='pending' AND next_attempt_at<=? ORDER BY sequence LIMIT 1) RETURNING delivery_id,event_type,action,payload,attempts,sequence`, now.UnixMilli()).Scan(&d.ID, &d.EventType, &d.Action, &d.Payload, &d.Attempts, &d.Sequence)
	return d, e
}
func (s *Store) Finish(ctx context.Context, d Delivery, err error, retry bool, delay time.Duration) error {
	if err == nil {
		_, e := s.DB.ExecContext(ctx, `UPDATE webhook_deliveries SET status='processed',processed_at=?,last_error='' WHERE delivery_id=?`, time.Now().UnixMilli(), d.ID)
		return e
	}
	status := "failed"
	if retry {
		status = "pending"
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE webhook_deliveries SET status=?,next_attempt_at=?,last_error=? WHERE delivery_id=?`, status, time.Now().Add(delay).UnixMilli(), err.Error(), d.ID)
	return e
}

// Retain the delivery ID forever so pruning payloads never permits redelivery duplicates.
func (s *Store) Cleanup(ctx context.Context, before time.Time) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE webhook_deliveries SET payload=x'' WHERE status='processed' AND processed_at<? AND length(payload)>0`, before.UnixMilli())
	return e
}
func (s *Store) Resolution(ctx context.Context, repo int64, kind, key, ref string) (int64, bool, error) {
	var id int64
	e := s.DB.QueryRowContext(ctx, `SELECT task_id FROM object_resolutions WHERE repository_id=? AND kind=? AND external_key=? AND task_ref=?`, repo, kind, key, ref).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, e == nil, e
}
func (s *Store) SaveResolution(ctx context.Context, repo int64, kind, key, ref string, id int64) error {
	_, e := s.DB.ExecContext(ctx, `INSERT INTO object_resolutions VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, repo, kind, key, ref, id)
	return e
}
func (s *Store) BranchInitialized(ctx context.Context, repo int64, branch string) (bool, error) {
	var b bool
	e := s.DB.QueryRowContext(ctx, `SELECT initialized FROM branches WHERE repository_id=? AND name=?`, repo, branch).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return b, e
}
func (s *Store) BranchState(ctx context.Context, repo int64, branch, state string, sequence int64, initialized bool) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `INSERT INTO branches VALUES(?,?,?,?,?) ON CONFLICT(repository_id,name) DO UPDATE SET initialized=max(branches.initialized,excluded.initialized),state=CASE WHEN excluded.sequence>=branches.sequence THEN excluded.state ELSE branches.state END,sequence=max(branches.sequence,excluded.sequence)`, repo, branch, initialized, state, sequence)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE github_references SET state=(SELECT state FROM branches WHERE repository_id=? AND name=?),updated_at=? WHERE repository_id=? AND kind='branch' AND external_key=?`, repo, branch, time.Now().UnixMilli(), repo, branch)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE managed_comments SET dirty=1 WHERE repository_id=? AND branch_name=?`, repo, branch)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) ObjectTasks(ctx context.Context, repo int64, kind, key string) (map[int64]string, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT task_id,matched_task_ref FROM github_references WHERE repository_id=? AND kind=? AND external_key=?`, repo, kind, key)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	m := map[int64]string{}
	for rows.Next() {
		var id int64
		var ref string
		if e = rows.Scan(&id, &ref); e != nil {
			return nil, e
		}
		m[id] = ref
	}
	return m, rows.Err()
}
func (s *Store) Upsert(ctx context.Context, r model.Reference, group string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if r.MetadataJSON == "" {
		r.MetadataJSON = "{}"
	}
	var id int64
	e = tx.QueryRowContext(ctx, `INSERT INTO github_references(task_id,matched_task_ref,repository_id,repository_full_name,repository_url,kind,external_key,url,title,state,branch_name,metadata_json,occurred_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(task_id,repository_id,kind,external_key) DO UPDATE SET repository_full_name=excluded.repository_full_name,repository_url=excluded.repository_url,url=excluded.url,title=CASE WHEN excluded.occurred_at>=github_references.occurred_at THEN excluded.title ELSE github_references.title END,state=CASE WHEN excluded.occurred_at>=github_references.occurred_at THEN excluded.state ELSE github_references.state END,metadata_json=CASE WHEN excluded.occurred_at>=github_references.occurred_at THEN excluded.metadata_json ELSE github_references.metadata_json END,occurred_at=max(github_references.occurred_at,excluded.occurred_at),updated_at=excluded.updated_at RETURNING id`, r.TaskID, r.MatchedTaskRef, r.Repository.ID, r.Repository.FullName, r.Repository.URL, r.Kind, r.ExternalKey, r.URL, r.Title, r.State, r.BranchName, r.MetadataJSON, r.OccurredAt.UnixMilli(), now, now).Scan(&id)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO reference_groups VALUES(?,?) ON CONFLICT DO NOTHING`, id, group)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO managed_comments(task_id,repository_id,branch_name,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(task_id,repository_id,branch_name) DO UPDATE SET dirty=1`, r.TaskID, r.Repository.ID, group, now, now)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) DirtyGroups(ctx context.Context) ([]model.Group, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT task_id,repository_id,branch_name FROM managed_comments WHERE dirty=1 ORDER BY task_id,repository_id,branch_name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var groups []model.Group
	for rows.Next() {
		var g model.Group
		if e = rows.Scan(&g.TaskID, &g.RepositoryID, &g.BranchName); e != nil {
			return nil, e
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}
func (s *Store) References(ctx context.Context, g model.Group) ([]model.Reference, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT r.task_id,r.matched_task_ref,r.repository_id,r.repository_full_name,r.repository_url,r.kind,r.external_key,r.url,r.title,r.state,r.branch_name,r.metadata_json,r.occurred_at FROM github_references r JOIN reference_groups g ON g.reference_id=r.id WHERE r.task_id=? AND r.repository_id=? AND g.branch_name=? ORDER BY r.occurred_at DESC,r.id DESC`, g.TaskID, g.RepositoryID, g.BranchName)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Reference
	for rows.Next() {
		var r model.Reference
		var ms int64
		if e = rows.Scan(&r.TaskID, &r.MatchedTaskRef, &r.Repository.ID, &r.Repository.FullName, &r.Repository.URL, &r.Kind, &r.ExternalKey, &r.URL, &r.Title, &r.State, &r.BranchName, &r.MetadataJSON, &ms); e != nil {
			return nil, e
		}
		r.OccurredAt = time.UnixMilli(ms)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Comment(ctx context.Context, g model.Group) (int64, string, error) {
	var id int64
	var hash string
	e := s.DB.QueryRowContext(ctx, `SELECT comment_id,content_hash FROM managed_comments WHERE task_id=? AND repository_id=? AND branch_name=?`, g.TaskID, g.RepositoryID, g.BranchName).Scan(&id, &hash)
	return id, hash, e
}
func (s *Store) SaveComment(ctx context.Context, g model.Group, id int64, hash string) error {
	if id <= 0 {
		return fmt.Errorf("invalid comment ID")
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE managed_comments SET comment_id=?,content_hash=?,dirty=0,updated_at=? WHERE task_id=? AND repository_id=? AND branch_name=?`, id, hash, time.Now().UnixMilli(), g.TaskID, g.RepositoryID, g.BranchName)
	return e
}

// GroupsForObject includes earlier branch memberships when a PR changes its head
// or a commit is observed on another branch.
func (s *Store) GroupsForObject(ctx context.Context, task, repo int64, kind, key string) ([]model.Group, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT g.branch_name FROM reference_groups g JOIN github_references r ON r.id=g.reference_id WHERE r.task_id=? AND r.repository_id=? AND r.kind=? AND r.external_key=?`, task, repo, kind, key)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Group
	for rows.Next() {
		g := model.Group{TaskID: task, RepositoryID: repo}
		if e = rows.Scan(&g.BranchName); e != nil {
			return nil, e
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) GroupsForBranch(ctx context.Context, repo int64, branch string) ([]model.Group, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT task_id FROM managed_comments WHERE repository_id=? AND branch_name=?`, repo, branch)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Group
	for rows.Next() {
		g := model.Group{RepositoryID: repo, BranchName: branch}
		if e = rows.Scan(&g.TaskID); e != nil {
			return nil, e
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) BranchStatus(ctx context.Context, repo int64, branch string) (string, error) {
	var state string
	e := s.DB.QueryRowContext(ctx, `SELECT state FROM branches WHERE repository_id=? AND name=?`, repo, branch).Scan(&state)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return state, e
}
