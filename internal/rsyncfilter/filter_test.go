package rsyncfilter

import (
	"bytes"
	"testing"

	"github.com/gokrazy/rsync/internal/rsyncwire"
)

func singleRule(t *testing.T, line string) *List {
	t.Helper()
	l := New()
	if err := l.Parse(line); err != nil {
		t.Fatalf("Parse(%q): %v", line, err)
	}
	return l
}

func assertMatch(t *testing.T, l *List, path string, isDir, wantInclude, wantMatched bool) {
	t.Helper()
	inc, matched := l.Match(path, isDir)
	if inc != wantInclude || matched != wantMatched {
		t.Errorf("Match(%q, isDir=%v) = (inc=%v, matched=%v), want (%v, %v)",
			path, isDir, inc, matched, wantInclude, wantMatched)
	}
}

func mustParseAll(t *testing.T, lines ...string) *List {
	t.Helper()
	l := New()
	for _, line := range lines {
		if err := l.Parse(line); err != nil {
			t.Fatalf("Parse(%q): %v", line, err)
		}
	}
	return l
}

func TestParseExcludeInclude(t *testing.T) {
	assertMatch(t, singleRule(t, "- *.log"), "noisy.log", false, false, true)
	assertMatch(t, singleRule(t, "+ *.go"), "main.go", false, true, true)
	// Bare pattern (no prefix) is an exclude under XFLG_OLD_PREFIXES.
	assertMatch(t, singleRule(t, "foo"), "foo", false, false, true)
}

func TestExcludeIncludeHelpers(t *testing.T) {
	l := New()
	if err := l.Exclude("*.log"); err != nil {
		t.Fatal(err)
	}
	if err := l.Include("*.go"); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, l, "noisy.log", false, false, true)
	assertMatch(t, l, "main.go", false, true, true)
}

func TestResetClearsList(t *testing.T) {
	l := mustParseAll(t, "- a", "- b", "!", "- c")
	// Rules added before "!" must no longer match; rules after still do.
	assertMatch(t, l, "a", false, true, false)
	assertMatch(t, l, "b", false, true, false)
	assertMatch(t, l, "c", false, false, true)
	if l.Len() != 1 {
		t.Errorf("Len after reset+one = %d, want 1", l.Len())
	}
}

func TestMatchAnchoredVsFloating(t *testing.T) {
	assertMatch(t, singleRule(t, "- /foo"), "foo", false, false, true)
	assertMatch(t, singleRule(t, "- /foo"), "sub/foo", false, true, false)
	// Floating, no '/' in pattern => basename match at any depth.
	assertMatch(t, singleRule(t, "- foo"), "a/b/foo", false, false, true)
}

func TestMatchDirectoryOnly(t *testing.T) {
	// A dir-only rule leaves a same-named file alone.
	assertMatch(t, singleRule(t, "- build/"), "build", false, true, false)
	assertMatch(t, singleRule(t, "- build/"), "build", true, false, true)
}

func TestMatchFirstRuleWins(t *testing.T) {
	l := mustParseAll(t, "+ *.go", "- *")
	assertMatch(t, l, "main.go", false, true, true)
	assertMatch(t, l, "main.py", false, false, true)
}

func TestMatchSlashInPatternUsesFullPath(t *testing.T) {
	assertMatch(t, singleRule(t, "- dir/thing"), "dir/thing", false, false, true)
	assertMatch(t, singleRule(t, "- dir/thing"), "thing", false, true, false)
}

func TestMatchDoubleStar(t *testing.T) {
	// `**` crosses slashes, including zero segments.
	l := singleRule(t, "- src/**/*.tmp")
	assertMatch(t, l, "src/foo.tmp", false, false, true)
	assertMatch(t, l, "src/a/b/foo.tmp", false, false, true)
	assertMatch(t, l, "src/keep.go", false, true, false)
	assertMatch(t, l, "other/a/foo.tmp", false, true, false)
}

func TestNilListDefaultIncludes(t *testing.T) {
	var l *List
	assertMatch(t, l, "anything", false, true, false)
	if l.Len() != 0 {
		t.Errorf("nil.Len() = %d, want 0", l.Len())
	}
}

func TestWildmatchSingleStarDoesNotCrossSlash(t *testing.T) {
	if wildmatch("a/*/c", "a/b/x/c") {
		t.Errorf("single * must not cross '/'")
	}
	if !wildmatch("a/*/c", "a/b/c") {
		t.Errorf("single * must match one non-slash segment")
	}
}

func TestWildmatchQuestionMark(t *testing.T) {
	if !wildmatch("a?c", "abc") {
		t.Errorf("? must match one char")
	}
	if wildmatch("a?c", "a/c") {
		t.Errorf("? must not match '/'")
	}
}

func TestWildmatchCharClass(t *testing.T) {
	cases := []struct {
		pat, in string
		want    bool
	}{
		{"[abc]", "a", true},
		{"[abc]", "d", false},
		{"[a-c]", "b", true},
		{"[!ab]", "x", true},
		{"[!ab]", "a", false},
		{"[^ab]", "a", false}, // ^ synonym for !
		// Trailing '-' is literal (no closing bound) — the range
		// check needs p[i+2] != ']'.
		{"[a-]", "-", true},
		{"[a-]", "a", true},
		{"[a-]", "b", false},
	}
	for _, c := range cases {
		if got := wildmatch(c.pat, c.in); got != c.want {
			t.Errorf("wildmatch(%q, %q)=%v, want %v", c.pat, c.in, got, c.want)
		}
	}
}

func TestWildmatchEscape(t *testing.T) {
	if !wildmatch(`a\*b`, "a*b") {
		t.Errorf(`\* should match literal '*'`)
	}
	if wildmatch(`a\*b`, "axb") {
		t.Errorf(`\* should not match wildcard`)
	}
}

func TestCanonicalRoundtrip(t *testing.T) {
	// canonical → parseRule → canonical must be stable, and the
	// resulting rule matches identically. This is the on-the-wire
	// contract Send relies on.
	lines := []string{
		"- *.log",
		"+ *.go",
		"- /anchored",
		"- build/",
		"+ /src/**/*.go",
		"!",
	}
	for _, line := range lines {
		r, err := parseRule(line)
		if err != nil {
			t.Fatalf("parseRule(%q): %v", line, err)
		}
		rt, err := parseRule(r.canonical())
		if err != nil {
			t.Fatalf("re-parse %q: %v", r.canonical(), err)
		}
		if r.canonical() != rt.canonical() {
			t.Errorf("canonical drift: %q → %q", r.canonical(), rt.canonical())
		}
	}
}

// TestSendRecvRoundtrip asserts that Send followed by Recv reconstructs
// rules that behave identically under Match. We deliberately compare on
// behavior, not internal flags — a future canonical-format change that
// preserves matching shouldn't break the test.
func TestSendRecvRoundtrip(t *testing.T) {
	orig := mustParseAll(t,
		"- *.log",
		"+ /keep.txt",
		"- build/",
		"+ /src/**/*.go",
		"- *",
	)

	buf := &bytes.Buffer{}
	c := &rsyncwire.Conn{Writer: buf, Reader: buf}
	if err := Send(c, orig); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := Recv(c)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}

	probes := []struct {
		path  string
		isDir bool
	}{
		{"noisy.log", false}, {"keep.txt", false},
		{"build", true}, {"src/a/b/main.go", false},
		{"other/main.go", false},
	}
	for _, p := range probes {
		wi, wm := orig.Match(p.path, p.isDir)
		gi, gm := got.Match(p.path, p.isDir)
		if wi != gi || wm != gm {
			t.Errorf("%q/%v: orig=(%v,%v) recv=(%v,%v)", p.path, p.isDir, wi, wm, gi, gm)
		}
	}
}

func TestSendNilActsLikeEmpty(t *testing.T) {
	buf := &bytes.Buffer{}
	c := &rsyncwire.Conn{Writer: buf, Reader: buf}
	if err := Send(c, nil); err != nil {
		t.Fatal(err)
	}
	got, err := Recv(c)
	if err != nil {
		t.Fatal(err)
	}
	// Whatever was sent, the received list must behave like an empty
	// one — any path default-includes with matched=false.
	assertMatch(t, got, "anything", false, true, false)
	if got.Len() != 0 {
		t.Errorf("Recv.Len() = %d, want 0", got.Len())
	}
}
