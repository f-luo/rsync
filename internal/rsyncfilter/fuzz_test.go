package rsyncfilter

import "testing"

// FuzzParseCanonicalRoundtrip asserts the Parse/Canonical contract
// in terms of observable outcomes, not internal fields: for any line
// Parse accepts, Parse(Canonical(rule)) produces a rule that, placed
// in a list, matches every path in behaviorCorpus the same way the
// original does.
func FuzzParseCanonicalRoundtrip(f *testing.F) {
	seeds := []string{
		"", "!", "- ", "+ ", "- *", "+ /foo/", "- **/build/",
		"\x00", "- \x00", "[bad", `\`, "foo//bar", "- /",
		"+ /src/**/*.go", "- *.log",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		r, err := Parse(line)
		if err != nil {
			return
		}
		rt, err := Parse(r.Canonical())
		if err != nil {
			t.Fatalf("Canonical(%q)=%q re-parse failed: %v", line, r.Canonical(), err)
		}
		// Classification must survive the roundtrip.
		if r.IsInclude() != rt.IsInclude() || r.IsReset() != rt.IsReset() {
			t.Fatalf("roundtrip changed IsInclude/IsReset for %q", line)
		}
		// Reset rules clear the list — test them in a populated-list context.
		probe, err := Parse("- probe")
		if err != nil {
			t.Fatalf("Parse(\"- probe\"): %v", err)
		}
		a, b := New(), New()
		if r.IsReset() {
			a.Add(probe)
			b.Add(probe)
		}
		a.Add(r)
		b.Add(rt)
		assertListsEquivalentT(t, a, b)
	})
}

// FuzzMatchNoPanic ensures Match never panics, regardless of the
// parsed rule or path input. We explicitly do NOT assert a particular
// match outcome — that is what the table-driven tests are for.
func FuzzMatchNoPanic(f *testing.F) {
	f.Add("- *.go", "main.go")
	f.Add("+ /keep/", "keep")
	f.Add("- src/**/*.tmp", "src/a/b/c.tmp")
	f.Fuzz(func(t *testing.T, line, path string) {
		r, err := Parse(line)
		if err != nil {
			return
		}
		l := New()
		l.Add(r)
		_, _ = l.Match(path, false)
		_, _ = l.Match(path, true)
	})
}

// assertListsEquivalentT is the *testing.T counterpart to
// assertListsEquivalent in wire_test.go. It t.Fatal's rather than
// t.Error's because fuzz discovery is more useful when the first
// divergence short-circuits further noise for the same input.
func assertListsEquivalentT(t *testing.T, a, b *List) {
	t.Helper()
	for _, tc := range behaviorCorpus {
		ai, am := a.Match(tc.path, tc.isDir)
		bi, bm := b.Match(tc.path, tc.isDir)
		if ai != bi || am != bm {
			t.Fatalf("Match(%q, isDir=%v) diverged: a=(%v,%v) b=(%v,%v)",
				tc.path, tc.isDir, ai, am, bi, bm)
		}
	}
}
