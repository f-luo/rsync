package rsyncfilter

import (
	"bytes"
	"testing"

	"github.com/gokrazy/rsync/internal/rsyncwire"
)

// pipeConn returns a Conn whose writes are readable from the same
// conn, so Send on one side can be Recv'd right back.
func pipeConn() *rsyncwire.Conn {
	buf := &bytes.Buffer{}
	return &rsyncwire.Conn{Writer: buf, Reader: buf}
}

// behaviorCorpus is the path/isDir battery used to compare two
// filter lists for observable equivalence. It covers every dimension
// matchesPath cares about: basename vs full-path, anchored vs
// floating, file vs dir, multi-depth paths, extension globs, and
// globstar zero- and multi-segment matches.
var behaviorCorpus = []struct {
	path  string
	isDir bool
}{
	{"", false}, {"", true},
	{"a", false}, {"a", true},
	{"a.log", false},
	{"a.go", false},
	{"keep.txt", false},
	{"build", false}, {"build", true},
	{"node_modules", true},
	{"src/a.go", false},
	{"src/a/b/c.go", false},
	{"src/a/b/c.tmp", false},
	{"a/build/x", false}, {"a/build", true},
	{"deep/nested/dir/file.md", false},
	{"#comment.txt", false},
}

// assertListsEquivalent asserts that a and b produce identical
// Match(path, isDir) outcomes across the behavior corpus. This is
// the contract Send/Recv is supposed to preserve.
func assertListsEquivalent(t *testing.T, a, b *List) {
	t.Helper()
	for _, tc := range behaviorCorpus {
		ai, am := a.Match(tc.path, tc.isDir)
		bi, bm := b.Match(tc.path, tc.isDir)
		if ai != bi || am != bm {
			t.Errorf("Match(%q, isDir=%v) diverged: orig=(%v,%v) recv=(%v,%v)",
				tc.path, tc.isDir, ai, am, bi, bm)
		}
	}
}

func TestRecvEmptyList(t *testing.T) {
	c := pipeConn()
	if err := c.WriteInt32(0); err != nil {
		t.Fatal(err)
	}
	got, err := Recv(c)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	// Empty list: every path falls through with default include.
	assertListsEquivalent(t, New(), got)
}

func TestSendRecvPreservesMatchingBehavior(t *testing.T) {
	// A list exercising every significant rule shape.
	orig := New()
	for _, line := range []string{
		"- *.log",
		"+ /keep.txt",
		"- build/",
		"+ /src/**/*.go",
		"- node_modules/",
		"+ *.md",
		"- *",
	} {
		orig.Add(mustParse(t, line))
	}

	c := pipeConn()
	if err := Send(c, orig); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := Recv(c)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}

	// The CONTRACT: Recv'd list matches identically to the sent list.
	// We do NOT compare internal flag/pattern fields — a future wire
	// format change that still preserves matching behavior should not
	// break this test.
	assertListsEquivalent(t, orig, got)
}

func TestSendNilActsLikeEmptyList(t *testing.T) {
	c := pipeConn()
	if err := Send(c, nil); err != nil {
		t.Fatalf("Send(nil): %v", err)
	}
	got, err := Recv(c)
	if err != nil {
		t.Fatalf("Recv after nil Send: %v", err)
	}
	assertListsEquivalent(t, New(), got)
}

func TestRecvRejectsNegativeLength(t *testing.T) {
	c := pipeConn()
	if err := c.WriteInt32(-5); err != nil {
		t.Fatal(err)
	}
	if _, err := Recv(c); err == nil {
		t.Errorf("Recv accepted negative length; want error")
	}
}

// TestCanonical pins the rsync-wire-compatible text format that
// Canonical produces. The SPECIFIC output strings matter here: they
// are what peer rsync implementations parse off the wire. If you
// change Canonical's output, a peer rsync client will either parse
// it differently or reject it — so these strings are an interop
// contract, not an implementation detail.
func TestCanonical(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"- foo", "- foo"},
		{"+ foo", "+ foo"},
		{"foo", "- foo"}, // bare pattern is canonicalised with explicit '-'
		{"!", "!"},
		{"- /foo", "- /foo"},
		{"- foo/", "- foo/"},
		{"- /foo/", "- /foo/"},
		{"+ /src/**/*.go", "+ /src/**/*.go"},
	}
	for _, tc := range tests {
		r, err := Parse(tc.line)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Canonical(); got != tc.want {
			t.Errorf("Parse(%q).Canonical() = %q, want %q (wire format)",
				tc.line, got, tc.want)
		}
	}
}

// TestCanonicalRoundtripPreservesBehavior is the behavioral dual of
// TestCanonical: regardless of the exact text Canonical emits,
// Parse(Canonical(rule)) must produce an observationally equivalent
// rule (same include/reset classification, and — for non-reset rules
// — same matching behavior in a list). A future rewrite of the
// canonical format that still satisfies this property should not
// break the contract.
func TestCanonicalRoundtripPreservesBehavior(t *testing.T) {
	lines := []string{
		"- *.log", "+ *.go", "- /anchored", "+ /anchored/",
		"- build/", "+ /src/**/*.go", "- foo",
	}
	for _, line := range lines {
		orig := mustParse(t, line)
		rt, err := Parse(orig.Canonical())
		if err != nil {
			t.Fatalf("Parse(Canonical(%q))=%q: %v", line, orig.Canonical(), err)
		}
		if orig.IsInclude() != rt.IsInclude() || orig.IsReset() != rt.IsReset() {
			t.Errorf("roundtrip %q: IsInclude/IsReset classification changed", line)
		}
		origList, rtList := New(), New()
		origList.Add(orig)
		rtList.Add(rt)
		assertListsEquivalent(t, origList, rtList)
	}

	// Reset rule: verify behavior on a populated list instead.
	pre := New()
	pre.Add(mustParse(t, "- drop"))
	pre.Add(mustParse(t, "!")) // should clear

	resetCanonical := mustParse(t, "!").Canonical()
	rt, err := Parse(resetCanonical)
	if err != nil {
		t.Fatalf("Parse(Canonical(!)): %v", err)
	}
	post := New()
	post.Add(mustParse(t, "- drop"))
	post.Add(rt)
	assertListsEquivalent(t, pre, post)
}
