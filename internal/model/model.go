// Package model contains transport-independent GitHub activity.
package model

import "time"

type Repository struct {
	ID            int64
	FullName, URL string
}
type BranchEvent struct {
	Repository  Repository
	Name, State string
}
type CommitEvent struct {
	Repository                Repository
	Branch, SHA, URL, Message string
	OccurredAt                time.Time
}
type PullRequestEvent struct {
	Repository                          Repository
	HeadRepositoryID                    int64
	Number                              int
	URL, Title, Body, HeadBranch, State string
	UpdatedAt                           time.Time
}
type Event struct {
	Repository  Repository
	Action      string
	Branch      *BranchEvent
	Commits     []CommitEvent
	PullRequest *PullRequestEvent
}
type Reference struct {
	TaskID                                                         int64
	MatchedTaskRef                                                 string
	Repository                                                     Repository
	Kind, ExternalKey, URL, Title, State, BranchName, MetadataJSON string
	OccurredAt                                                     time.Time
}
type Group struct {
	TaskID, RepositoryID int64
	BranchName           string
}
