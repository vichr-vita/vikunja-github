package taskref

import (
	"reflect"
	"testing"
)

func TestExtract(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"LEDGER-42", []string{"LEDGER-42"}}, {"ledger-42 fix", []string{"LEDGER-42"}}, {"feat/LEDGER-42-foo", []string{"LEDGER-42"}}, {"fix(LEDGER-42): correct matching", []string{"LEDGER-42"}}, {"LEDGER-42 LEDGER-51", []string{"LEDGER-42", "LEDGER-51"}}, {"LEDGER-42,ledger-042", []string{"LEDGER-42"}}, {"fooLEDGER-42bar", nil}, {"42 #42 LEDGER-", nil}, {"_LEDGER-42 LEDGER-42_", nil}, {"LEDGER-0 LEDGER-9999999999999999999999", nil}, {"A2-7", []string{"A2-7"}},
	} {
		t.Run(tt.in, func(t *testing.T) {
			var got []string
			for _, r := range ExtractTaskRefs(tt.in) {
				got = append(got, r.Key())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}
func TestCustom(t *testing.T) {
	m, e := New(`(?i)\[([A-Z]+):(\d+)\]`)
	if e != nil {
		t.Fatal(e)
	}
	if got := m.Extract("[home:2]"); len(got) != 1 || got[0].Project != "HOME" {
		t.Fatal(got)
	}
	for _, s := range []string{`(`, `\d+`, `(a)(b)(c)`} {
		if _, e = New(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
