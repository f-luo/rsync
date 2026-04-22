package rsyncfilter

import (
	"bufio"
	"io"
	"strings"
)

// AddFromReader reads one rule per line from r and appends each to l.
// Blank lines and lines whose first non-whitespace character is '#' or
// ';' are ignored. Lines without an explicit "- "/"+ "/"!" prefix are
// treated as includes when defaultInclude is true, excludes otherwise —
// matching --include-from / --exclude-from semantics.
//
// options.c:parse_filter_file
func (l *List) AddFromReader(r io.Reader, defaultInclude bool) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		t := strings.TrimLeft(strings.TrimRight(sc.Text(), "\r"), " \t")
		if t == "" || t[0] == '#' || t[0] == ';' {
			continue
		}
		if t != "!" && !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "+ ") {
			sign := "- "
			if defaultInclude {
				sign = "+ "
			}
			t = sign + t
		}
		if err := l.Parse(t); err != nil {
			return err
		}
	}
	return sc.Err()
}
