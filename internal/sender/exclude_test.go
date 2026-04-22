package sender

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gokrazy/rsync/internal/rsyncwire"
)

// singleRule parses line and returns a list containing just that
// rule, used to assert parseFilter produces rules whose Match
// behavior is correct — without reaching into rule internals.
func singleRule(t *testing.T, line string) *FilterRuleList {
	t.Helper()
	fr, err := parseFilter(line)
	if err != nil {
		t.Fatalf("parseFilter(%q): %v", line, err)
	}
	l := &FilterRuleList{}
	l.addRule(fr)
	return l
}

func assertMatch(t *testing.T, l *FilterRuleList, path string, isDir, wantInclude, wantMatched bool) {
	t.Helper()
	inc, matched := l.Match(path, isDir)
	if inc != wantInclude || matched != wantMatched {
		t.Errorf("Match(%q, isDir=%v) = (inc=%v, matched=%v), want (%v, %v)",
			path, isDir, inc, matched, wantInclude, wantMatched)
	}
}

func TestParseExcludeInclude(t *testing.T) {
	assertMatch(t, singleRule(t, "- *.log"), "noisy.log", false, false, true)
	assertMatch(t, singleRule(t, "+ *.go"), "main.go", false, true, true)
	// Bare pattern (no prefix) is an exclude under XFLG_OLD_PREFIXES.
	assertMatch(t, singleRule(t, "foo"), "foo", false, false, true)
}

func TestParseReset(t *testing.T) {
	l := &FilterRuleList{}
	a, _ := parseFilter("- a")
	b, _ := parseFilter("- b")
	reset, _ := parseFilter("!")
	l.addRule(a)
	l.addRule(b)
	// The reset rule carries filtruleClearList; the caller (rsyncopts
	// / AddFromReader) empties the list when it sees one.
	if reset.flag&filtruleClearList == 0 {
		t.Fatalf("parseFilter(!) did not set clear-list flag")
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
	l := &FilterRuleList{}
	inc, _ := parseFilter("+ *.go")
	ex, _ := parseFilter("- *")
	l.addRule(inc)
	l.addRule(ex)

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
	// canonical → parseFilter → canonical must be stable, and the
	// resulting rule matches identically. This is the on-the-wire
	// contract SendFilterList relies on.
	lines := []string{
		"- *.log",
		"+ *.go",
		"- /anchored",
		"- build/",
		"+ /src/**/*.go",
		"!",
	}
	for _, line := range lines {
		fr, err := parseFilter(line)
		if err != nil {
			t.Fatalf("parseFilter(%q): %v", line, err)
		}
		rt, err := parseFilter(fr.canonical())
		if err != nil {
			t.Fatalf("re-parse %q: %v", fr.canonical(), err)
		}
		if fr.canonical() != rt.canonical() {
			t.Errorf("canonical drift: %q → %q", fr.canonical(), rt.canonical())
		}
	}
}

// TestSendRecvRoundtrip asserts that SendFilterList followed by
// RecvFilterList reconstructs rules that behave identically under
// Match. We deliberately compare on behavior, not internal flags —
// a future canonical-format change that preserves matching shouldn't
// break the test.
func TestSendRecvRoundtrip(t *testing.T) {
	orig := &FilterRuleList{}
	for _, line := range []string{
		"- *.log",
		"+ /keep.txt",
		"- build/",
		"+ /src/**/*.go",
		"- *",
	} {
		fr, err := parseFilter(line)
		if err != nil {
			t.Fatal(err)
		}
		orig.addRule(fr)
	}

	buf := &bytes.Buffer{}
	c := &rsyncwire.Conn{Writer: buf, Reader: buf}
	if err := SendFilterList(c, orig); err != nil {
		t.Fatalf("SendFilterList: %v", err)
	}
	got, err := RecvFilterList(c)
	if err != nil {
		t.Fatalf("RecvFilterList: %v", err)
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
	if err := SendFilterList(c, nil); err != nil {
		t.Fatal(err)
	}
	got, err := RecvFilterList(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Filters) != 0 {
		t.Errorf("len(Filters)=%d, want 0", len(got.Filters))
	}
	// Empty list matches nothing; default include.
	inc, matched := got.Match("anything", false)
	if !inc || matched {
		t.Errorf("empty list: Match=(%v,%v), want (true,false)", inc, matched)
	}
}

func TestAddFromReaderSkipsCommentsAndBlanks(t *testing.T) {
	input := `# a comment
; another comment

*.log
`
	l := &FilterRuleList{}
	if err := l.AddFromReader(strings.NewReader(input), false); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, l, "noisy.log", false, false, true)
	assertMatch(t, l, "# a comment", false, true, false)
	assertMatch(t, l, "", false, true, false)
}

func TestAddFromReaderDefaultSign(t *testing.T) {
	ex := &FilterRuleList{}
	if err := ex.AddFromReader(strings.NewReader("a\nb\n"), false); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, ex, "a", false, false, true)

	inc := &FilterRuleList{}
	if err := inc.AddFromReader(strings.NewReader("a\n"), true); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, inc, "a", false, true, true)
}

func TestAddFromReaderHonorsExplicitPrefixes(t *testing.T) {
	input := "+ keep.go\n- drop.go\n"
	l := &FilterRuleList{}
	if err := l.AddFromReader(strings.NewReader(input), false); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, l, "keep.go", false, true, true)
	assertMatch(t, l, "drop.go", false, false, true)
}

func TestAddFromReaderStripsCRLF(t *testing.T) {
	l := &FilterRuleList{}
	if err := l.AddFromReader(strings.NewReader("*.log\r\n"), false); err != nil {
		t.Fatal(err)
	}
	// `*.log\r` would not match `noisy.log`; the '\r' must be stripped.
	assertMatch(t, l, "noisy.log", false, false, true)
}

func TestParseFilterRules(t *testing.T) {
	rules := []string{"- *.log", "+ keep.go", "- /build/"}
	l, err := ParseFilterRules(rules)
	if err != nil {
		t.Fatal(err)
	}
	assertMatch(t, l, "noisy.log", false, false, true)
	assertMatch(t, l, "keep.go", false, true, true)
	assertMatch(t, l, "build", true, false, true)
}
