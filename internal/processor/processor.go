package processor

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"

	"vikunja-github/internal/model"
	"vikunja-github/internal/presentation"
	"vikunja-github/internal/store"
	"vikunja-github/internal/taskref"
	"vikunja-github/internal/vikunja"
)

type Vikunja interface {
	ResolveTask(context.Context, string, int64) (vikunja.Task, error)
	CreateComment(context.Context, int64, string) (vikunja.Comment, error)
	UpdateComment(context.Context, int64, int64, string) (vikunja.Comment, error)
	FindComment(context.Context, int64, string) (vikunja.Comment, error)
}
type Processor struct {
	Store      *store.Store
	API        Vikunja
	Matcher    *taskref.Matcher
	Projects   map[string]bool
	MaxCommits int
	Log        *slog.Logger
}

// Process can be retried after any step. All identities and comment memberships
// are durable; no network call runs inside a database transaction.
func (p *Processor) Process(ctx context.Context, event model.Event, sequence int64) error {
	groups := map[model.Group]bool{}
	add := func(r model.Reference, branch string) error {
		if e := p.Store.Upsert(ctx, r, branch); e != nil {
			return e
		}
		existing, e := p.Store.GroupsForObject(ctx, r.TaskID, r.Repository.ID, r.Kind, r.ExternalKey)
		if e != nil {
			return e
		}
		for _, g := range existing {
			groups[g] = true
		}
		p.Log.Debug("github_reference_updated", "task_id", r.TaskID, "reference_kind", r.Kind, "reference_key", r.ExternalKey)
		return nil
	}
	branchTasks := map[int64]string{}
	if b := event.Branch; b != nil {
		var e error
		branchTasks, e = p.Store.ObjectTasks(ctx, b.Repository.ID, "branch", b.Name)
		if e != nil {
			return e
		}
		if b.State != "deleted" {
			initialized, e := p.Store.BranchInitialized(ctx, b.Repository.ID, b.Name)
			if e != nil {
				return e
			}
			if !initialized {
				fresh, e := p.resolve(ctx, b.Repository.ID, "branch", b.Name, b.Name)
				if e != nil {
					return e
				}
				merge(branchTasks, fresh)
			}
			for id, ref := range branchTasks {
				if e = add(model.Reference{TaskID: id, MatchedTaskRef: ref, Repository: b.Repository, Kind: "branch", ExternalKey: b.Name, URL: presentation.BranchURL(b.Repository.URL, b.Name), State: b.State, BranchName: b.Name}, b.Name); e != nil {
					return e
				}
			}
		}
		if e = p.Store.BranchState(ctx, b.Repository.ID, b.Name, b.State, sequence, b.State != "deleted"); e != nil {
			return e
		}
		affected, e := p.Store.GroupsForBranch(ctx, b.Repository.ID, b.Name)
		if e != nil {
			return e
		}
		for _, g := range affected {
			groups[g] = true
		}
	}
	for _, c := range event.Commits {
		tasks, e := p.Store.ObjectTasks(ctx, c.Repository.ID, "commit", c.SHA)
		if e != nil {
			return e
		}
		fresh, e := p.resolve(ctx, c.Repository.ID, "commit", c.SHA, c.Message)
		if e != nil {
			return e
		}
		merge(tasks, fresh)
		merge(tasks, branchTasks)
		metadata, _ := json.Marshal(map[string]string{"sha": c.SHA, "branch": c.Branch})
		for id, ref := range tasks {
			group := ""
			if _, ok := branchTasks[id]; ok {
				group = c.Branch
			}
			if e = add(model.Reference{TaskID: id, MatchedTaskRef: ref, Repository: c.Repository, Kind: "commit", ExternalKey: c.SHA, URL: c.URL, Title: c.Message, BranchName: c.Branch, OccurredAt: c.OccurredAt, MetadataJSON: string(metadata)}, group); e != nil {
				return e
			}
		}
	}
	if pr := event.PullRequest; pr != nil {
		key := strconv.Itoa(pr.Number)
		tasks, e := p.Store.ObjectTasks(ctx, pr.Repository.ID, "pull_request", key)
		if e != nil {
			return e
		}
		// Fork branches inherit only from their actual repository, never from a
		// same-named branch in the base repository.
		inherited, e := p.Store.ObjectTasks(ctx, pr.HeadRepositoryID, "branch", pr.HeadBranch)
		if e != nil {
			return e
		}
		merge(tasks, inherited)
		initialized, e := p.Store.BranchInitialized(ctx, pr.HeadRepositoryID, pr.HeadBranch)
		if e != nil {
			return e
		}
		explicit := pr.Title + "\n" + pr.Body
		if !initialized {
			explicit += "\n" + pr.HeadBranch
		}
		fresh, e := p.resolve(ctx, pr.Repository.ID, "pull_request", key, explicit)
		if e != nil {
			return e
		}
		merge(tasks, fresh)
		group := pr.HeadBranch
		if pr.HeadRepositoryID != pr.Repository.ID {
			group = ""
		}
		metadata, _ := json.Marshal(map[string]any{"head_repository_id": pr.HeadRepositoryID, "head_branch": pr.HeadBranch})
		for id, ref := range tasks {
			if e = add(model.Reference{TaskID: id, MatchedTaskRef: ref, Repository: pr.Repository, Kind: "pull_request", ExternalKey: key, URL: pr.URL, Title: pr.Title, State: pr.State, BranchName: pr.HeadBranch, MetadataJSON: string(metadata), OccurredAt: pr.UpdatedAt}, group); e != nil {
				return e
			}
		}
	}
	// Deterministic order makes partial failures and logs easier to inspect.
	ordered := make([]model.Group, 0, len(groups))
	for g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.TaskID != b.TaskID {
			return a.TaskID < b.TaskID
		}
		if a.RepositoryID != b.RepositoryID {
			return a.RepositoryID < b.RepositoryID
		}
		return a.BranchName < b.BranchName
	})
	for _, g := range ordered {
		if e := p.syncComment(ctx, g); e != nil {
			return e
		}
	}
	return nil
}
func merge(dst, src map[int64]string) {
	for id, ref := range src {
		if _, ok := dst[id]; !ok {
			dst[id] = ref
		}
	}
}
func (p *Processor) resolve(ctx context.Context, repo int64, kind, key, text string) (map[int64]string, error) {
	tasks := map[int64]string{}
	for _, r := range p.Matcher.Extract(text) {
		if len(p.Projects) > 0 && !p.Projects[r.Project] {
			continue
		}
		id, known, e := p.Store.Resolution(ctx, repo, kind, key, r.Key())
		if e != nil {
			return nil, e
		}
		if !known {
			p.Log.Debug("task_ref_found", "task_ref", r.Key(), "reference_kind", kind, "reference_key", key)
			t, e := p.API.ResolveTask(ctx, r.Project, r.Index)
			if e != nil && !vikunja.IsStatus(e, 404) {
				return nil, e
			}
			if e != nil {
				p.Log.Warn("task_ref_unresolved", "task_ref", r.Key(), "reference_kind", kind, "reference_key", key)
			} else {
				id = t.ID
			}
			if e = p.Store.SaveResolution(ctx, repo, kind, key, r.Key(), id); e != nil {
				return nil, e
			}
		}
		if id > 0 {
			tasks[id] = r.Key()
		}
	}
	return tasks, nil
}
func (p *Processor) syncComment(ctx context.Context, g model.Group) error {
	refs, e := p.Store.References(ctx, g)
	if e != nil {
		return e
	}
	if g.BranchName != "" {
		state, err := p.Store.BranchStatus(ctx, g.RepositoryID, g.BranchName)
		if err != nil {
			return err
		}
		if state != "" {
			hasBranch := false
			for _, r := range refs {
				if r.Kind == "branch" {
					hasBranch = true
					break
				}
			}
			if !hasBranch && len(refs) > 0 {
				refs = append(refs, model.Reference{Kind: "branch", State: state, Repository: refs[0].Repository, URL: presentation.BranchURL(refs[0].Repository.URL, g.BranchName)})
			}
		}
	}
	content := presentation.Render(g, refs, p.MaxCommits)
	hash := presentation.Hash(content)
	id, oldHash, e := p.Store.Comment(ctx, g)
	if e != nil {
		return e
	}
	if id > 0 && oldHash == hash {
		return p.Store.SaveComment(ctx, g, id, hash)
	}
	if id == 0 {
		found, e := p.API.FindComment(ctx, g.TaskID, presentation.Marker(g))
		if e != nil {
			return e
		}
		id = found.ID
		if id == 0 {
			created, e := p.API.CreateComment(ctx, g.TaskID, content)
			if e != nil {
				return e
			}
			id = created.ID
			p.Log.Info("vikunja_comment_created", "task_id", g.TaskID, "comment_id", id)
			return p.Store.SaveComment(ctx, g, id, hash)
		}
	}
	_, e = p.API.UpdateComment(ctx, g.TaskID, id, content)
	if vikunja.IsStatus(e, 404) {
		// The managed comment may have been deleted manually. Reconcile once before
		// replacement, just as for an uncertain POST result.
		found, findErr := p.API.FindComment(ctx, g.TaskID, presentation.Marker(g))
		if findErr != nil {
			return findErr
		}
		if found.ID > 0 {
			_, e = p.API.UpdateComment(ctx, g.TaskID, found.ID, content)
			id = found.ID
		} else {
			var created vikunja.Comment
			created, e = p.API.CreateComment(ctx, g.TaskID, content)
			id = created.ID
		}
	}
	if e != nil {
		return e
	}
	p.Log.Info("vikunja_comment_updated", "task_id", g.TaskID, "comment_id", id)
	return p.Store.SaveComment(ctx, g, id, hash)
}
