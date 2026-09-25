package config

import "testing"

func TestConfig(t *testing.T) {
	t.Setenv("VIKUNJA_BASE_URL", "https://tasks.example.com")
	t.Setenv("VIKUNJA_TOKEN", "test")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "test")
	t.Setenv("ALLOWED_REPOSITORIES", " Owner/Repo,other/repo ")
	t.Setenv("ALLOWED_PROJECT_IDENTIFIERS", "ledger,Home")
	c, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if !c.Repositories["owner/repo"] || !c.Projects["LEDGER"] || c.MaxAttempts != 10 {
		t.Fatal(c.MaxAttempts)
	}
	for _, tt := range []struct{ k, v string }{{"VIKUNJA_TOKEN", ""}, {"GITHUB_WEBHOOK_SECRET", ""}, {"VIKUNJA_BASE_URL", "ftp://host"}, {"VIKUNJA_BASE_URL", "https://user:password@host"}, {"MAX_RETRY_ATTEMPTS", "0"}, {"MAX_COMMITS_IN_COMMENT", "bad"}, {"TASK_REF_REGEX", "(bad)"}, {"LOG_LEVEL", "bad"}} {
		t.Run(tt.k+tt.v, func(t *testing.T) {
			t.Setenv(tt.k, tt.v)
			if _, e := Load(); e == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
