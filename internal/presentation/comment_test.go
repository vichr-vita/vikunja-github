package presentation

import (
	"strings"
	"testing"
	"vikunja-github/internal/model"
)

func TestEscapeAndSafeURLs(t *testing.T) {
	g := model.Group{TaskID: 1, RepositoryID: 2}
	s := Render(g, []model.Reference{{Repository: model.Repository{FullName: "owner/repo", URL: "https://github.com/owner/repo"}, Kind: "commit", ExternalKey: "abcdef123", Title: "<script> @somebody [click](evil)", URL: "javascript:alert(1)"}}, 10)
	for _, bad := range []string{"<script>", "@somebody", "javascript:"} {
		if strings.Contains(s, bad) {
			t.Fatal(s)
		}
	}
	if !strings.Contains(s, Marker(g)) {
		t.Fatal("missing marker")
	}
	if Marker(g) == Marker(model.Group{TaskID: 1, RepositoryID: 2, BranchName: "other"}) {
		t.Fatal("marker collision")
	}
}
