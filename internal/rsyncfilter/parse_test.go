package rsyncfilter

import (
	"strings"
	"testing"
)

// singleRule(line) parses `line` as a filter rule and returns a List
// containing just that rule. Used to assert that Parse produces rules
// with the right matching behavior, without inspecting Rule internals.
func singleRule(t *testing.T, line string) *List {
	t.Helper()
	r, err := Parse(line)
	if err != nil {
		t.Fatalf("Parse(%q): %v", line, err)
	}
	l := New()
	l.Add(r)
	return l
}

// assertMatch feeds (path, isDir) through l.Match and asserts on
// (include, matched). This is the surface callers care about.
func assertMatch(t *testing.T, l *List, path string, isDir, wantInclude, wantMatched bool) {
	t.Helper()
	inc, matched := l.Match(path, isDir)
	if inc != wantInclude || matched != wantMatched {
		t.Errorf("Match(%q, isDir=%v) = (inc=%v, matched=%v), want (%v, %v)",
			path, isDir, inc, matched, wantInclude, wantMatched)
	}
}

func TestParseExcludeRuleExcludes(t *testing.T) {
	l := singleRule(t, "- *.log")
	assertMatch(t, l, "noisy.log", false, false, true)
	assertMatch(t, l, "README.md", false, true, false) // fall-through
}

func TestParseIncludeRuleIncludes(t *testing.T) {
	l := singleRule(t, "+ *.go")
	assertMatch(t, l, "main.go", false, true, true)
}

func TestParseBarePatternIsExclude(t *testing.T) {
	// No prefix => exclude (XFLG_OLD_PREFIXES default).
	l := singleRule(t, "foo")
	assertMatch(t, l, "foo", false, false, true)
}

func TestParseResetRule(t *testing.T) {
	// The "!" rule is not added to the list; it clears it.
	l := New()
	l.Add(mustParse(t, "- a"))
	l.Add(mustParse(t, "- b"))
	l.Add(mustParse(t, "!"))
	// After reset, nothing matches — the list is empty.
	assertMatch(t, l, "a", false, true, false)
	assertMatch(t, l, "b", false, true, false)
	if l.Len() != 0 {
		t.Errorf("after '!': Len()=%d, want 0", l.Len())
	}
}

func TestParseAnchoredRuleMatchesOnlyAtRoot(t *testing.T) {
	l := singleRule(t, "- /foo")
	assertMatch(t, l, "foo", false, false, true)
	assertMatch(t, l, "sub/foo", false, true, false)
}

func TestParseFloatingRuleMatchesAtAnyDepth(t *testing.T) {
	l := singleRule(t, "- foo")
	assertMatch(t, l, "foo", false, false, true)
	assertMatch(t, l, "a/b/foo", false, false, true) // basename match
}

func TestParseDirectoryOnlyRuleIgnoresFiles(t *testing.T) {
	l := singleRule(t, "- build/")
	assertMatch(t, l, "build", false, true, false) // file named build: pass through
	assertMatch(t, l, "build", true, false, true)  // dir named build: excluded
}

func TestParseExcludeIncludeShortcuts(t *testing.T) {
	// The contract of ParseExclude / ParseInclude is "behaves like
	// Parse('- '+p) / Parse('+ '+p)". Test the observable behavior.
	ex, err := ParseExclude("*.log")
	if err != nil {
		t.Fatal(err)
	}
	inc, err := ParseInclude("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if ex.IsInclude() {
		t.Errorf("ParseExclude yielded an include rule")
	}
	if !inc.IsInclude() {
		t.Errorf("ParseInclude yielded an exclude rule")
	}
	// Spot-check they actually match.
	l := New()
	l.Add(ex)
	assertMatch(t, l, "a.log", false, false, true)
	l2 := New()
	l2.Add(inc)
	assertMatch(t, l2, "a.go", false, true, true)
}

func TestAddFromReaderSkipsCommentsAndBlanks(t *testing.T) {
	input := `# a comment
; another comment

*.log
`
	l := New()
	if err := l.AddFromReader(strings.NewReader(input), false); err != nil {
		t.Fatal(err)
	}
	// Exactly one effective rule got added; verify by behavior.
	assertMatch(t, l, "noisy.log", false, false, true)
	assertMatch(t, l, "# a comment", false, true, false) // comment wasn't treated as a pattern
	assertMatch(t, l, "", false, true, false)            // blank line didn't become a rule
}

func TestAddFromReaderHonorsExplicitPrefixes(t *testing.T) {
	// Explicit "+"/"-" prefixes win over the defaultInclude argument.
	input := "+ keep.go\n- drop.go\n"
	l := New()
	if err := l.AddFromReader(strings.NewReader(input), false); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, l, "keep.go", false, true, true)
	assertMatch(t, l, "drop.go", false, false, true)
}

func TestAddFromReaderDefaultSign(t *testing.T) {
	// Unsigned lines: defaultInclude=false → excludes; true → includes.
	ex := New()
	if err := ex.AddFromReader(strings.NewReader("a\nb\n"), false); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, ex, "a", false, false, true)
	assertMatch(t, ex, "b", false, false, true)

	inc := New()
	if err := inc.AddFromReader(strings.NewReader("a\nb\n"), true); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, inc, "a", false, true, true)
	assertMatch(t, inc, "b", false, true, true)
}

func TestAddFromReaderResetRule(t *testing.T) {
	input := "- first\n!\n- second\n"
	l := New()
	if err := l.AddFromReader(strings.NewReader(input), false); err != nil {
		t.Fatal(err)
	}
	// Only `- second` survived the reset.
	assertMatch(t, l, "first", false, true, false)
	assertMatch(t, l, "second", false, false, true)
}

func TestAddFromReaderStripsCRLF(t *testing.T) {
	// CRLF must not leak into patterns: `*.log\r` would not match
	// `noisy.log` because doublestar sees a literal '\r' at the end.
	l := New()
	if err := l.AddFromReader(strings.NewReader("*.log\r\n"), false); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, l, "noisy.log", false, false, true)
}
