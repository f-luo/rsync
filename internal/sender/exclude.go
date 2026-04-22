package sender

import (
	"bufio"
	"bytes"
	"io"
	"path/filepath"
	"strings"

	"github.com/gokrazy/rsync/internal/rsyncwire"
)

type FilterRuleList struct {
	Filters []*filterRule
}

// exclude.c:add_rule
func (l *FilterRuleList) addRule(fr *filterRule) {
	// A "!" rule clears the list and is not itself retained —
	// mirrors exclude.c:parse_rule_tok when filtruleClearList is set.
	if fr.flag&filtruleClearList != 0 {
		l.Filters = nil
		return
	}
	l.Filters = append(l.Filters, fr)
}

// matches reports whether any rule in the list excludes name. Retained
// for callers that do not have isDir in hand; prefer Match where
// possible because directory-only rules can only be evaluated
// correctly with isDir.
//
// exclude.c:check_filter
func (l *FilterRuleList) matches(name string) bool {
	include, matched := l.Match(name, false)
	return matched && !include
}

// Match walks the list in order and returns the outcome of the first
// rule that matches (path, isDir). include is true for a '+' rule,
// false for a '-' rule. matched is false if no rule matched, in which
// case include defaults to true (rsync's default-include fall-through).
//
// exclude.c:check_filter
func (l *FilterRuleList) Match(path string, isDir bool) (include, matched bool) {
	if l == nil {
		return true, false
	}
	for _, fr := range l.Filters {
		if fr.matches(path, isDir) {
			return fr.flag&filtruleInclude != 0, true
		}
	}
	return true, false
}

// exclude.c:recv_filter_list
func RecvFilterList(c *rsyncwire.Conn) (*FilterRuleList, error) {
	var l FilterRuleList
	const exclusionListEnd = 0
	for {
		length, err := c.ReadInt32()
		if err != nil {
			return nil, err
		}
		if length == exclusionListEnd {
			break
		}
		line := make([]byte, length)
		if _, err := io.ReadFull(c.Reader, line); err != nil {
			return nil, err
		}
		fr, err := parseFilter(string(line))
		if err != nil {
			return nil, err
		}
		l.addRule(fr)
	}
	return &l, nil
}

// SendFilterList writes l to c in rsync's wire format, terminated by
// a zero-length entry. Each rule is serialised in its canonical form
// so that a peer's recv_filter_list re-parses the same flags.
//
// exclude.c:send_filter_list
func SendFilterList(c *rsyncwire.Conn, l *FilterRuleList) error {
	if l != nil {
		for _, fr := range l.Filters {
			text := fr.canonical()
			if err := c.WriteInt32(int32(len(text))); err != nil {
				return err
			}
			if err := c.WriteString(text); err != nil {
				return err
			}
		}
	}
	return c.WriteInt32(0)
}

// ParseFilterRules parses rules in XFLG_OLD_PREFIXES form — the same
// strings rsyncopts.Options.FilterRules returns — into a
// FilterRuleList suitable for Match and SendFilterList.
func ParseFilterRules(rules []string) (*FilterRuleList, error) {
	l := &FilterRuleList{}
	for _, line := range rules {
		fr, err := parseFilter(line)
		if err != nil {
			return nil, err
		}
		l.addRule(fr)
	}
	return l, nil
}

// AddFromReader reads filter rules from r, one per line, and appends
// them to l. Blank lines and lines whose first non-whitespace
// character is '#' or ';' are skipped. Lines with no "- "/"+ "/"!"
// prefix take the sign given by defaultInclude (true → include,
// false → exclude) — this mirrors rsync's --include-from (defaults
// include) vs --exclude-from (defaults exclude).
//
// exclude.c:parse_filter_file
func (l *FilterRuleList) AddFromReader(r io.Reader, defaultInclude bool) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		t := strings.TrimLeft(line, " \t")
		if t == "" || t[0] == '#' || t[0] == ';' {
			continue
		}
		var ruleLine string
		switch {
		case t == "!",
			strings.HasPrefix(t, "- "),
			strings.HasPrefix(t, "+ "):
			ruleLine = t
		case defaultInclude:
			ruleLine = "+ " + t
		default:
			ruleLine = "- " + t
		}
		fr, err := parseFilter(ruleLine)
		if err != nil {
			return err
		}
		l.addRule(fr)
	}
	return sc.Err()
}

const (
	filtruleInclude = 1 << iota
	filtruleClearList
	filtruleDirectory
	filtruleWild
	filtruleAnchored
)

type filterRule struct {
	flag    int
	pattern string
}

// matches reports whether fr matches (path, isDir). Pattern matching
// uses rsync's wildmatch semantics (see wildmatch below); anchoring,
// directory-only scoping, and basename-vs-fullpath target selection
// follow exclude.c:rule_matches.
//
// exclude.c:rule_matches
func (fr *filterRule) matches(path string, isDir bool) bool {
	if fr.flag&filtruleDirectory != 0 && !isDir {
		return false
	}
	target := path
	if fr.flag&filtruleAnchored == 0 && !strings.ContainsRune(fr.pattern, '/') {
		target = filepath.Base(path)
	}
	return wildmatch(fr.pattern, target)
}

// canonical returns fr's on-the-wire text form — the inverse of
// parseFilter. The result always carries an explicit "- "/"+ " sign
// (or is exactly "!" for the clear-list rule), so a peer recovers
// the same flags by re-parsing.
func (fr *filterRule) canonical() string {
	if fr.flag&filtruleClearList != 0 {
		return "!"
	}
	var b strings.Builder
	if fr.flag&filtruleInclude != 0 {
		b.WriteString("+ ")
	} else {
		b.WriteString("- ")
	}
	if fr.flag&filtruleAnchored != 0 {
		b.WriteByte('/')
	}
	b.WriteString(fr.pattern)
	if fr.flag&filtruleDirectory != 0 {
		b.WriteByte('/')
	}
	return b.String()
}

// exclude.c:parse_filter_str / exclude.c:parse_rule_tok
func parseFilter(line string) (*filterRule, error) {
	rule := new(filterRule)

	// We only support what rsync calls XFLG_OLD_PREFIXES.
	switch {
	case strings.HasPrefix(line, "- "):
		line = line[2:]
	case strings.HasPrefix(line, "+ "):
		rule.flag |= filtruleInclude
		line = line[2:]
	case line == "!":
		rule.flag |= filtruleClearList
		return rule, nil
	}

	if strings.HasPrefix(line, "/") {
		rule.flag |= filtruleAnchored
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		rule.flag |= filtruleDirectory
		line = line[:len(line)-1]
	}
	if strings.ContainsAny(line, "*?[") {
		rule.flag |= filtruleWild
	}

	rule.pattern = line
	return rule, nil
}

// wildmatch reports whether pattern matches text using rsync's
// shell-glob semantics (wildmatch.c:dowild):
//
//   - '?' matches any one character except '/'.
//   - '*' matches any run of characters except '/'.
//   - '**' matches any run of characters including '/'.
//   - '[...]' matches one character from a class; '!' or '^' after
//     '[' negates; 'a-z' is a range; ']' as first char is literal.
//   - '\c' matches the literal character c.
//
// wildmatch.c:dowild
func wildmatch(pattern, text string) bool {
	return doWild([]byte(pattern), []byte(text))
}

func doWild(p, t []byte) bool {
	for {
		if len(p) == 0 {
			return len(t) == 0
		}
		switch p[0] {
		case '?':
			if len(t) == 0 || t[0] == '/' {
				return false
			}
			p, t = p[1:], t[1:]
		case '\\':
			if len(p) < 2 || len(t) == 0 || t[0] != p[1] {
				return false
			}
			p, t = p[2:], t[1:]
		case '[':
			if len(t) == 0 || t[0] == '/' {
				return false
			}
			ok, size := matchClass(p, t[0])
			if !ok {
				return false
			}
			p, t = p[size:], t[1:]
		case '*':
			// Consecutive '*'s collapse; two or more cross '/'.
			starCount := 0
			for len(p) > 0 && p[0] == '*' {
				starCount++
				p = p[1:]
			}
			crossSlash := starCount >= 2
			if len(p) == 0 {
				if crossSlash {
					return true
				}
				return bytes.IndexByte(t, '/') < 0
			}
			// '**/' can match zero path segments: try matching the
			// tail of the pattern past the '/' at the current text
			// position. Mirrors wildmatch.c's zero-segment collapse.
			if crossSlash && p[0] == '/' {
				if doWild(p[1:], t) {
					return true
				}
			}
			for i := 0; ; i++ {
				if doWild(p, t[i:]) {
					return true
				}
				if i >= len(t) {
					return false
				}
				if !crossSlash && t[i] == '/' {
					return false
				}
			}
		default:
			if len(t) == 0 || t[0] != p[0] {
				return false
			}
			p, t = p[1:], t[1:]
		}
	}
}

// matchClass evaluates a '[...]' character class against c. It
// returns whether c matched and the number of pattern bytes consumed
// (including the leading '[' and trailing ']'). An unclosed class
// never matches.
func matchClass(p []byte, c byte) (matched bool, size int) {
	if len(p) < 2 || p[0] != '[' {
		return false, 0
	}
	i := 1
	neg := false
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		neg = true
		i++
	}
	first := true
	for i < len(p) {
		if !first && p[i] == ']' {
			i++
			if neg {
				matched = !matched
			}
			return matched, i
		}
		first = false
		switch {
		case p[i] == '\\' && i+1 < len(p):
			if p[i+1] == c {
				matched = true
			}
			i += 2
		case i+2 < len(p) && p[i+1] == '-' && p[i+2] != ']':
			if c >= p[i] && c <= p[i+2] {
				matched = true
			}
			i += 3
		default:
			if p[i] == c {
				matched = true
			}
			i++
		}
	}
	return false, 0
}
