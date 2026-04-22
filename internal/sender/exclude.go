package sender

import (
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
	if fr.flag&filtruleClearList != 0 {
		l.Filters = nil
		return
	}
	l.Filters = append(l.Filters, fr)
}

// Match implements receiver.FilterList.
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

// ParseFilterRules parses rules in XFLG_OLD_PREFIXES form — the strings
// rsyncopts.Options.FilterRules returns — into a FilterRuleList.
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

const (
	filtruleInclude = 1 << iota
	filtruleClearList
	filtruleDirectory
	filtruleAnchored
)

type filterRule struct {
	flag    int
	pattern string
}

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

// canonical returns fr's on-the-wire text — the inverse of parseFilter.
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
	rule.pattern = line
	return rule, nil
}

// wildmatch reports whether pattern matches text under rsync shell-glob
// semantics: '?' and '*' stop at '/', '**' crosses '/', '[...]' classes
// (with '!'/'^' negation and 'a-z' ranges), and '\c' escapes.
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

// matchClass evaluates a '[...]' character class against c, returning
// whether c matched and the number of pattern bytes consumed. An
// unclosed class returns (false, 0).
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
