package presentation

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"vikunja-github/internal/model"
)

func Marker(g model.Group) string {
	return fmt.Sprintf("vikunja-github:%x", sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%s", g.TaskID, g.RepositoryID, g.BranchName))))
}
func Hash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

// Escape untrusted GitHub text and break @mentions to avoid notifying Vikunja users.
func text(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "@", "@\u200b")
	return r.Replace(s)
}
func link(label, raw string) string {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return text(label)
	}
	safe := strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E", " ", "%20").Replace(u.String())
	return "[" + text(label) + "](" + safe + ")"
}
func BranchURL(repoURL, branch string) string {
	return strings.TrimRight(repoURL, "/") + "/tree/" + url.PathEscape(branch)
}
func Render(g model.Group, refs []model.Reference, maxCommits int) string {
	var b strings.Builder
	b.WriteString("### GitHub development\n\n")
	if len(refs) > 0 {
		fmt.Fprintf(&b, "Repository: %s\n\n", link(refs[0].Repository.FullName, refs[0].Repository.URL))
	}
	if g.BranchName != "" {
		branchURL := ""
		state := "active"
		for _, r := range refs {
			if r.Kind == "branch" {
				branchURL = r.URL
				state = r.State
				break
			}
		}
		if branchURL == "" && len(refs) > 0 {
			branchURL = BranchURL(refs[0].Repository.URL, g.BranchName)
		}
		fmt.Fprintf(&b, "Branch: %s\n\nStatus: %s\n\n", link(g.BranchName, branchURL), text(state))
	}
	prs := 0
	for _, r := range refs {
		if r.Kind == "pull_request" {
			if prs == 0 {
				b.WriteString("Pull requests:\n")
			}
			fmt.Fprintf(&b, "- %s · %s\n", link("#"+r.ExternalKey+" "+r.Title, r.URL), text(r.State))
			prs++
		}
	}
	if prs > 0 {
		b.WriteString("\n")
	}
	total, shown := 0, 0
	for _, r := range refs {
		if r.Kind != "commit" {
			continue
		}
		total++
		if shown >= maxCommits {
			continue
		}
		if shown == 0 {
			b.WriteString("Commits:\n")
		}
		sha := r.ExternalKey
		if len(sha) > 7 {
			sha = sha[:7]
		}
		title := strings.SplitN(r.Title, "\n", 2)[0]
		fmt.Fprintf(&b, "- %s %s\n", link(sha, r.URL), text(title))
		shown++
	}
	if total > 0 {
		fmt.Fprintf(&b, "\n%d commits total. Showing the latest %d.\n", total, shown)
	}
	fmt.Fprintf(&b, "\nManaged by vikunja-github. `%s`\n", Marker(g))
	return b.String()
}
