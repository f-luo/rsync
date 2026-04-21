package rsyncfilter

import (
	"bufio"
	"io"
	"strings"
)

// Parse parses a single filter rule line in rsync's XFLG_OLD_PREFIXES
// form. Recognised prefixes are "- " (exclude), "+ " (include), and
// "!" (clear list). A line with no prefix is treated as an exclude.
//
// A leading '/' on the pattern anchors the rule to the transfer root
// (and is stripped). A trailing '/' marks the rule as directory-only
// (and is stripped). Patterns containing '*', '?', or '[' are flagged
// as wildcards.
func Parse(line string) (*Rule, error) {
	r := &Rule{}

	switch {
	case line == "!":
		r.flag |= filtruleClearList
		return r, nil
	case strings.HasPrefix(line, "- "):
		line = line[2:]
	case strings.HasPrefix(line, "+ "):
		r.flag |= filtruleInclude
		line = line[2:]
	}

	if strings.HasPrefix(line, "/") {
		r.flag |= filtruleAnchored
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		r.flag |= filtruleDirectory
		line = line[:len(line)-1]
	}
	if strings.ContainsAny(line, "*?[") {
		r.flag |= filtruleWild
	}

	r.pattern = line
	return r, nil
}

// ParseExclude parses pattern as an exclude rule (equivalent to
// "- pattern").
func ParseExclude(pattern string) (*Rule, error) {
	return Parse("- " + pattern)
}

// ParseInclude parses pattern as an include rule (equivalent to
// "+ pattern").
func ParseInclude(pattern string) (*Rule, error) {
	return Parse("+ " + pattern)
}

// AddFromReader parses rules from r, one per line, and appends them
// to l. Blank lines and lines whose first non-whitespace character is
// '#' or ';' are skipped. Lines with no "- "/"+ " prefix inherit the
// sign given by defaultInclude (true => include, false => exclude).
func (l *List) AddFromReader(r io.Reader, defaultInclude bool) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		if trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}

		// Honour explicit "- "/"+ "/"!" prefixes; otherwise apply
		// the caller-supplied default sign.
		var ruleLine string
		switch {
		case trimmed == "!" ||
			strings.HasPrefix(trimmed, "- ") ||
			strings.HasPrefix(trimmed, "+ "):
			ruleLine = trimmed
		case defaultInclude:
			ruleLine = "+ " + trimmed
		default:
			ruleLine = "- " + trimmed
		}

		rule, err := Parse(ruleLine)
		if err != nil {
			return err
		}
		l.Add(rule)
	}
	return sc.Err()
}
