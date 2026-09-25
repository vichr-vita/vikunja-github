// Package taskref extracts human task references without assigning permanent identities.
package taskref

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const DefaultPattern = `(?i)\b([A-Z][A-Z0-9]*)-(\d+)\b`

type TaskRef struct {
	Project string
	Index   int64
	Raw     string
}

func (r TaskRef) Key() string { return fmt.Sprintf("%s-%d", r.Project, r.Index) }

type Matcher struct{ re *regexp.Regexp }

func New(pattern string) (*Matcher, error) {
	if pattern == "" {
		pattern = DefaultPattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	if re.NumSubexp() != 2 {
		return nil, fmt.Errorf("task reference regex must have exactly two capture groups: project and index")
	}
	return &Matcher{re}, nil
}
func (m *Matcher) Extract(s string) []TaskRef {
	var out []TaskRef
	seen := map[string]bool{}
	for _, v := range m.re.FindAllStringSubmatch(s, -1) {
		n, e := strconv.ParseInt(v[2], 10, 64)
		if e != nil || n <= 0 {
			continue
		}
		r := TaskRef{strings.ToUpper(v[1]), n, v[0]}
		if !seen[r.Key()] {
			seen[r.Key()] = true
			out = append(out, r)
		}
	}
	return out
}

var defaultMatcher, _ = New("")

func ExtractTaskRefs(s string) []TaskRef { return defaultMatcher.Extract(s) }
