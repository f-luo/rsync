package rsyncfilter

import (
	"strings"
	"testing"
)

func TestAddFromReaderDefaultInclude(t *testing.T) {
	// Shape mirrors an --include-from file: bare lines are includes,
	// "- " / "+ " / "!" keep their explicit sign. "# " and "; " are
	// comments; CRLF line endings are tolerated.
	in := "# keep list\nkeep.go\n+ also.go\n- drop.go\n\n"
	l := New()
	if err := l.AddFromReader(strings.NewReader(in), true); err != nil {
		t.Fatalf("AddFromReader: %v", err)
	}

	assertMatch(t, l, "keep.go", false, true, true)
	assertMatch(t, l, "also.go", false, true, true)
	assertMatch(t, l, "drop.go", false, false, true)
	if l.Len() != 3 {
		t.Errorf("Len = %d, want 3", l.Len())
	}
}

func TestAddFromReaderDefaultExclude(t *testing.T) {
	// --exclude-from shape: bare lines exclude, "!" resets, CRLF is
	// stripped, ';' is a comment marker.
	in := "; skip list\n*.log\r\n+ keep.log\n!\n- after-reset\n"
	l := New()
	if err := l.AddFromReader(strings.NewReader(in), false); err != nil {
		t.Fatalf("AddFromReader: %v", err)
	}

	// The "!" clears everything added above, so only "after-reset"
	// survives.
	if l.Len() != 1 {
		t.Errorf("Len = %d, want 1", l.Len())
	}
	assertMatch(t, l, "noisy.log", false, true, false) // rule was cleared
	assertMatch(t, l, "after-reset", false, false, true)
}

func TestAddFromReaderSkipsBlankAndComments(t *testing.T) {
	in := "\n   \n# comment\n; also comment\n  \tkeep.txt\n"
	l := New()
	if err := l.AddFromReader(strings.NewReader(in), true); err != nil {
		t.Fatal(err)
	}
	if l.Len() != 1 {
		t.Fatalf("Len = %d, want 1", l.Len())
	}
	assertMatch(t, l, "keep.txt", false, true, true)
}
