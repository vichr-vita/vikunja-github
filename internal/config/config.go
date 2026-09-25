package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"vikunja-github/internal/taskref"
)

type Config struct {
	BaseURL, Token, Secret, DatabasePath, ListenAddr, Pattern string
	LogLevel                                                  slog.Level
	MaxAttempts, RetentionDays, MaxCommits                    int
	Repositories, Projects                                    map[string]bool
}

func Load() (Config, error) {
	c := Config{BaseURL: strings.TrimRight(os.Getenv("VIKUNJA_BASE_URL"), "/"), Token: os.Getenv("VIKUNJA_TOKEN"), Secret: os.Getenv("GITHUB_WEBHOOK_SECRET"), DatabasePath: value("DATABASE_PATH", "/data/vikunja-github.db"), ListenAddr: value("LISTEN_ADDR", ":8080"), Pattern: value("TASK_REF_REGEX", taskref.DefaultPattern), Repositories: list(os.Getenv("ALLOWED_REPOSITORIES"), false), Projects: list(os.Getenv("ALLOWED_PROJECT_IDENTIFIERS"), true)}
	u, e := url.Parse(c.BaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("VIKUNJA_BASE_URL must be an HTTP(S) instance URL without credentials, query or fragment")
	}
	if c.Token == "" || c.Secret == "" {
		return c, fmt.Errorf("VIKUNJA_TOKEN and GITHUB_WEBHOOK_SECRET are required")
	}
	if e = c.LogLevel.UnmarshalText([]byte(value("LOG_LEVEL", "info"))); e != nil {
		return c, fmt.Errorf("invalid LOG_LEVEL")
	}
	for _, v := range []struct {
		name     string
		fallback int
		dest     *int
	}{{"MAX_RETRY_ATTEMPTS", 10, &c.MaxAttempts}, {"WEBHOOK_RETENTION_DAYS", 7, &c.RetentionDays}, {"MAX_COMMITS_IN_COMMENT", 10, &c.MaxCommits}} {
		n, err := strconv.Atoi(value(v.name, strconv.Itoa(v.fallback)))
		if err != nil || n < 1 {
			return c, fmt.Errorf("%s must be a positive integer", v.name)
		}
		*v.dest = n
	}
	if _, e = taskref.New(c.Pattern); e != nil {
		return c, e
	}
	return c, nil
}
func value(k, d string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return d
}
func list(s string, upper bool) map[string]bool {
	m := map[string]bool{}
	for _, v := range strings.Split(s, ",") {
		v = strings.TrimSpace(v)
		if upper {
			v = strings.ToUpper(v)
		} else {
			v = strings.ToLower(v)
		}
		if v != "" {
			m[v] = true
		}
	}
	return m
}
