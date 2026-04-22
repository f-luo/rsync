// Package rsyncfilter implements rsync's filter list: parsing rules,
// matching file paths against them, and exchanging them over the wire.
// It mirrors the subset of rsync's exclude.c and wildmatch.c that
// gokrazy/rsync supports today — "XFLG_OLD_PREFIXES"-form rules
// ("- PAT" / "+ PAT" / "!") with shell-glob patterns, anchoring
// ("/PAT"), and directory-only ("PAT/") modifiers.
package rsyncfilter

import (
	"path/filepath"
	"strings"
)

const (
	ruleInclude = 1 << iota
	ruleClearList
	ruleDirectory
	ruleAnchored
	ruleBasename // pattern has no '/' and is unanchored: match basename only
)

type rule struct {
	flag    int
	pattern string
}

// List is an ordered collection of filter rules. Order is significant:
// Match returns the decision of the first rule that matches.
type List struct {
	rules []*rule
}

// New returns an empty List.
func New() *List { return &List{} }

// Len reports the number of rules currently in the list.
func (l *List) Len() int {
	if l == nil {
		return 0
	}
	return len(l.rules)
}

// Parse parses a single rule line and appends it to the list. A bare
// pattern (no "- "/"+ "/"!" prefix) is treated as an exclude, matching
// rsync's XFLG_OLD_PREFIXES default. A "!" rule clears all previously
// added rules.
func (l *List) Parse(line string) error {
	r, err := parseRule(line)
	if err != nil {
		return err
	}
	l.add(r)
	return nil
}

// Exclude is shorthand for l.Parse("- " + pattern).
func (l *List) Exclude(pattern string) error {
	return l.Parse("- " + pattern)
}

// Include is shorthand for l.Parse("+ " + pattern).
func (l *List) Include(pattern string) error {
	return l.Parse("+ " + pattern)
}

// Match tests path against the list. include is true if path should be
// transferred; matched reports whether any rule explicitly matched (a
// fall-through path returns (true, false) so callers can distinguish a
// default include from an explicit one).
//
// A nil List default-includes everything.
//
// exclude.c:check_filter
func (l *List) Match(path string, isDir bool) (include, matched bool) {
	if l == nil {
		return true, false
	}
	for _, r := range l.rules {
		if r.matches(path, isDir) {
			return r.flag&ruleInclude != 0, true
		}
	}
	return true, false
}

// add appends r, except that a "!" clear-list rule discards all
// previously added rules instead of being retained.
//
// exclude.c:add_rule
func (l *List) add(r *rule) {
	if r.flag&ruleClearList != 0 {
		l.rules = nil
		return
	}
	l.rules = append(l.rules, r)
}

// matches reports whether r applies to path. Directory-only rules skip
// files; patterns without a '/' match on the basename so "*.log" hits
// at any depth.
//
// exclude.c:rule_matches
func (r *rule) matches(path string, isDir bool) bool {
	if r.flag&ruleDirectory != 0 && !isDir {
		return false
	}
	target := path
	if r.flag&ruleBasename != 0 {
		target = filepath.Base(path)
	}
	return wildmatch(r.pattern, target)
}

// canonical returns the on-the-wire text form of r — the inverse of
// parseRule. SendFilterList relies on this being stable across
// parse→canonical→parse roundtrips.
func (r *rule) canonical() string {
	if r.flag&ruleClearList != 0 {
		return "!"
	}
	var b strings.Builder
	if r.flag&ruleInclude != 0 {
		b.WriteString("+ ")
	} else {
		b.WriteString("- ")
	}
	if r.flag&ruleAnchored != 0 {
		b.WriteByte('/')
	}
	b.WriteString(r.pattern)
	if r.flag&ruleDirectory != 0 {
		b.WriteByte('/')
	}
	return b.String()
}

// parseRule parses a single XFLG_OLD_PREFIXES-form line into a rule.
//
// exclude.c:parse_filter_str / exclude.c:parse_rule_tok
func parseRule(line string) (*rule, error) {
	r := new(rule)
	switch {
	case strings.HasPrefix(line, "- "):
		line = line[2:]
	case strings.HasPrefix(line, "+ "):
		r.flag |= ruleInclude
		line = line[2:]
	case line == "!":
		r.flag |= ruleClearList
		return r, nil
	}
	if strings.HasPrefix(line, "/") {
		r.flag |= ruleAnchored
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		r.flag |= ruleDirectory
		line = line[:len(line)-1]
	}
	if r.flag&ruleAnchored == 0 && !strings.Contains(line, "/") {
		r.flag |= ruleBasename
	}
	r.pattern = line
	return r, nil
}
