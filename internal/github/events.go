// Package github owns webhook transport and payload normalization.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"vikunja-github/internal/model"
)

type repository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	URL      string `json:"html_url"`
}

func (r repository) model() model.Repository {
	return model.Repository{ID: r.ID, FullName: r.FullName, URL: r.URL}
}

type payload struct {
	Action     string     `json:"action"`
	Repository repository `json:"repository"`
	Ref        string     `json:"ref"`
	RefType    string     `json:"ref_type"`
	Deleted    bool       `json:"deleted"`
	Commits    []struct {
		ID        string    `json:"id"`
		Message   string    `json:"message"`
		URL       string    `json:"url"`
		Timestamp time.Time `json:"timestamp"`
	} `json:"commits"`
	Number int `json:"number"`
	PR     *struct {
		Number    int       `json:"number"`
		URL       string    `json:"html_url"`
		Title     string    `json:"title"`
		Body      string    `json:"body"`
		State     string    `json:"state"`
		Merged    bool      `json:"merged"`
		UpdatedAt time.Time `json:"updated_at"`
		Head      struct {
			Ref  string      `json:"ref"`
			Repo *repository `json:"repo"`
		} `json:"head"`
	} `json:"pull_request"`
}

func Normalize(event string, raw []byte) (model.Event, error) {
	var p payload
	var out model.Event
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return out, fmt.Errorf("payload must be an object")
	}
	if e := json.Unmarshal(raw, &p); e != nil {
		return out, fmt.Errorf("invalid payload JSON")
	}
	out.Repository = p.Repository.model()
	out.Action = p.Action
	switch event {
	case "create", "delete", "push", "pull_request":
		if p.Repository.ID <= 0 || p.Repository.FullName == "" {
			return out, fmt.Errorf("missing repository")
		}
	default:
		return out, nil
	}
	switch event {
	case "create", "delete":
		if p.RefType == "tag" {
			return out, nil
		}
		if p.RefType != "branch" || p.Ref == "" {
			return out, fmt.Errorf("missing branch ref")
		}
		state := "active"
		if event == "delete" {
			state = "deleted"
		}
		out.Branch = &model.BranchEvent{Repository: out.Repository, Name: p.Ref, State: state}
	case "push":
		if strings.HasPrefix(p.Ref, "refs/tags/") {
			return out, nil
		}
		if !strings.HasPrefix(p.Ref, "refs/heads/") || len(p.Ref) == len("refs/heads/") {
			return out, fmt.Errorf("invalid push ref")
		}
		branch := strings.TrimPrefix(p.Ref, "refs/heads/")
		state := "active"
		if p.Deleted {
			state = "deleted"
		}
		out.Branch = &model.BranchEvent{Repository: out.Repository, Name: branch, State: state}
		if p.Deleted {
			return out, nil
		}
		for _, c := range p.Commits {
			if c.ID == "" {
				return out, fmt.Errorf("missing commit SHA")
			}
			out.Commits = append(out.Commits, model.CommitEvent{Repository: out.Repository, Branch: branch, SHA: c.ID, URL: c.URL, Message: c.Message, OccurredAt: c.Timestamp})
		}
	case "pull_request":
		if p.PR == nil || p.PR.Number <= 0 || p.PR.Head.Ref == "" || (p.PR.State != "open" && p.PR.State != "closed") {
			return out, fmt.Errorf("invalid pull request")
		}
		state := p.PR.State
		if p.PR.Merged {
			state = "merged"
		}
		headID := int64(0)
		if p.PR.Head.Repo != nil {
			headID = p.PR.Head.Repo.ID
		}
		out.PullRequest = &model.PullRequestEvent{Repository: out.Repository, HeadRepositoryID: headID, Number: p.PR.Number, URL: p.PR.URL, Title: p.PR.Title, Body: p.PR.Body, HeadBranch: p.PR.Head.Ref, State: state, UpdatedAt: p.PR.UpdatedAt}
	}
	return out, nil
}
