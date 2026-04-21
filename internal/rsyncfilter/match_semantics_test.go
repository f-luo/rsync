package rsyncfilter

import "testing"

// TestDoublestarAssumptions pins down the glob-engine behaviors that
// OUR code depends on to match rsync semantics. If doublestar ever
// changes these, our package is wrong — so this test is a canary,
// not redundant coverage of doublestar itself.
//
// We deliberately do NOT exhaustively test doublestar's engine here
// (e.g. all character-class edge cases, escape sequences). Those
// have their own test suite upstream.
func TestDoublestarAssumptions(t *testing.T) {
	type c struct {
		name     string
		rule     string
		path     string
		isDir    bool
		wantExcl bool // we use '-' rules; want == true means excluded
	}
	cases := []c{
		// ASSUMPTION: '*' never crosses '/'.
		// Used by: any floating single-segment glob, e.g. `- *.log`.
		{"star-no-cross-slash", "- a/*/c", "a/b/x/c", false, false},
		{"star-within-segment", "- a/*/c", "a/b/c", false, true},

		// ASSUMPTION: '**' crosses '/'.
		// Used by: any multi-depth glob, e.g. `- src/**/*.tmp`.
		{"doublestar-crosses", "- a/**/c", "a/b/d/c", false, true},

		// ASSUMPTION: '**/' allows zero-segment matches.
		// Used by: `src/**/*.go` matching a file directly in src/.
		// This is NOT the bash default without globstar, so we rely
		// on doublestar's explicit support for it.
		{"doublestar-zero-seg", "- src/**/*.go", "src/main.go", false, true},

		// ASSUMPTION: '?' does not match '/'.
		// Used by: patterns like `a?c` restricted to one segment.
		{"question-no-cross-slash", "- a?c", "a/c", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := singleRule(t, tc.rule)
			inc, matched := l.Match(tc.path, tc.isDir)
			gotExcl := matched && !inc
			if gotExcl != tc.wantExcl {
				t.Errorf("rule %q vs %q: excluded=%v, want %v",
					tc.rule, tc.path, gotExcl, tc.wantExcl)
			}
		})
	}
}
