package rsyncfilter

import "testing"

func mustParse(t *testing.T, line string) *Rule {
	t.Helper()
	r, err := Parse(line)
	if err != nil {
		t.Fatalf("Parse(%q): %v", line, err)
	}
	return r
}

func TestMatchFirstRuleWins(t *testing.T) {
	l := New()
	l.Add(mustParse(t, "+ *.go"))
	l.Add(mustParse(t, "- *"))

	inc, matched := l.Match("main.go", false)
	if !inc || !matched {
		t.Errorf("main.go: include=%v matched=%v, want true,true", inc, matched)
	}
	inc, matched = l.Match("main.py", false)
	if inc || !matched {
		t.Errorf("main.py: include=%v matched=%v, want false,true", inc, matched)
	}
}

func TestMatchFallthroughDefaultInclude(t *testing.T) {
	l := New()
	l.Add(mustParse(t, "- *.log"))

	inc, matched := l.Match("README.md", false)
	if !inc || matched {
		t.Errorf("README.md: include=%v matched=%v, want true,false", inc, matched)
	}
}

func TestMatchDirectoryOnlyRule(t *testing.T) {
	l := New()
	l.Add(mustParse(t, "- build/"))

	// A file literally named "build" must NOT be excluded.
	if _, matched := l.Match("build", false); matched {
		t.Errorf("dir-only rule matched a regular file")
	}
	// A directory named "build" IS excluded.
	if inc, matched := l.Match("build", true); !matched || inc {
		t.Errorf("dir-only rule: include=%v matched=%v, want false,true", inc, matched)
	}
}

func TestMatchAnchoredVsFloating(t *testing.T) {
	l := New()
	l.Add(mustParse(t, "- /foo"))

	// Anchored => matches only at root.
	if _, matched := l.Match("foo", false); !matched {
		t.Errorf("anchored rule should match 'foo' at root")
	}
	if _, matched := l.Match("a/foo", false); matched {
		t.Errorf("anchored rule should not match 'a/foo'")
	}

	l2 := New()
	l2.Add(mustParse(t, "- foo"))
	// Floating => basename match, so matches at any depth.
	if _, matched := l2.Match("a/b/foo", false); !matched {
		t.Errorf("floating rule should match 'a/b/foo'")
	}
}

func TestMatchSlashInPatternUsesFullPath(t *testing.T) {
	l := New()
	l.Add(mustParse(t, "- dir/thing"))

	// A pattern containing '/' but not anchored still matches against
	// the full path (not basename), but not the anchored-only root.
	if _, matched := l.Match("dir/thing", false); !matched {
		t.Errorf("pattern with '/' should match full path")
	}
	if _, matched := l.Match("thing", false); matched {
		t.Errorf("pattern with '/' should not match plain 'thing'")
	}
}

func TestListAddReset(t *testing.T) {
	l := New()
	l.Add(mustParse(t, "- a"))
	l.Add(mustParse(t, "- b"))
	if l.Len() != 2 {
		t.Fatalf("Len() = %d before reset, want 2", l.Len())
	}
	l.Add(mustParse(t, "!"))
	if l.Len() != 0 {
		t.Errorf("Len() = %d after reset, want 0", l.Len())
	}
	// Reset rule itself is discarded, not appended.
}

func TestMatchesLegacy(t *testing.T) {
	// Matches() is the pre-refactor API used by sender/flist.go walkFn.
	// It returns true only when a rule explicitly excludes the path.
	l := New()
	// Include rules must come first so they carve exceptions out of
	// the later catch-all exclude.
	l.Add(mustParse(t, "+ important.log"))
	l.Add(mustParse(t, "- *.log"))

	if !l.Matches("noisy.log") {
		t.Errorf("Matches(noisy.log) = false, want true")
	}
	// important.log is included, not excluded, so Matches() returns false.
	if l.Matches("important.log") {
		t.Errorf("Matches(important.log) = true, want false (explicit +)")
	}
	// Default fall-through is include; Matches() returns false.
	if l.Matches("README.md") {
		t.Errorf("Matches(README.md) = true, want false (fall-through)")
	}
}

func TestDoubleStarMatch(t *testing.T) {
	l := New()
	l.Add(mustParse(t, "- src/**/*.tmp"))

	tests := []struct {
		path string
		want bool
	}{
		{"src/foo.tmp", true},        // zero-segment
		{"src/a/b/foo.tmp", true},    // multi-segment
		{"src/keep.go", false},       // different suffix
		{"other/a/foo.tmp", false},   // not under src
	}
	for _, tc := range tests {
		got := l.Matches(tc.path)
		if got != tc.want {
			t.Errorf("Matches(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
