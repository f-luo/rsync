// Package rsyncfilter is a port of rsync's exclude.c — parsing filter
// rules and matching file paths against them.
//
// Supported rule forms (XFLG_OLD_PREFIXES):
//
//   - PAT      exclude files matching PAT
//   + PAT      include files matching PAT
//   !          reset: clear all previously accumulated rules
//
// PAT uses shell-glob semantics (see wildmatch.go). A leading '/'
// anchors the pattern to the transfer root; a trailing '/' makes the
// rule directory-only.
//
// The package's v1 surface intentionally does not include the full
// --filter grammar (:merge, .dir-merge, P/S/R modifiers, --cvs-exclude);
// those are deferred per doc/filter-support.md.
package rsyncfilter

import (
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const (
	filtruleInclude = 1 << iota
	filtruleClearList
	filtruleDirectory
	filtruleWild
	filtruleAnchored
)

// Rule is a single parsed filter rule.
type Rule struct {
	flag    int
	pattern string
}

// IsInclude reports whether this rule is an include rule ('+').
func (r *Rule) IsInclude() bool { return r.flag&filtruleInclude != 0 }

// IsReset reports whether this rule is the '!' list-reset rule.
func (r *Rule) IsReset() bool { return r.flag&filtruleClearList != 0 }

// Pattern returns the raw pattern text (with anchoring and
// directory-only markers already stripped).
func (r *Rule) Pattern() string { return r.pattern }

// List is an ordered collection of filter rules. Order is
// significant: the first rule that matches a given path determines
// the outcome.
type List struct {
	filters []*Rule
}

// New returns an empty filter list.
func New() *List { return &List{} }

// Len returns the number of rules currently in the list.
func (l *List) Len() int { return len(l.filters) }

// Add appends r to the list. If r is the '!' reset rule, all
// previously added rules are removed and r itself is discarded.
func (l *List) Add(r *Rule) {
	if r.flag&filtruleClearList != 0 {
		l.filters = l.filters[:0]
		return
	}
	l.filters = append(l.filters, r)
}

// Match tests path against the list.
//
// path is the transfer-relative path, with forward slashes and no
// leading '/'. isDir is true when path refers to a directory.
//
// include is true if path should be transferred. matched is true if
// some rule explicitly matched; when matched is false, path fell
// through the list (default include).
func (l *List) Match(path string, isDir bool) (include, matched bool) {
	for _, r := range l.filters {
		if r.matchesPath(path, isDir) {
			return r.IsInclude(), true
		}
	}
	return true, false
}

// Matches reports whether any rule in the list excludes name.
//
// Retained for callers that walk over path names without the isDir
// context needed by Match. It assumes name refers to a regular file;
// directory-only rules therefore will not cause it to return true on
// a file. Callers with isDir in hand should prefer Match.
func (l *List) Matches(name string) bool {
	include, matched := l.Match(name, false)
	return matched && !include
}

// matchesPath reports whether r matches the given path. Pattern
// matching itself is delegated to github.com/bmatcuk/doublestar/v4,
// which implements bash-compatible globbing with '/' as the path
// separator: '*' and '?' do not cross '/', '**' does, and a bare
// '**' may match zero path segments.
//
// Anchoring, directory-only scoping, and basename-vs-fullpath target
// selection follow rsync's exclude.c:rule_matches.
func (r *Rule) matchesPath(path string, isDir bool) bool {
	if r.flag&filtruleDirectory != 0 && !isDir {
		return false
	}
	var target string
	switch {
	case r.flag&filtruleAnchored != 0:
		target = path
	case !strings.ContainsRune(r.pattern, '/'):
		target = filepath.Base(path)
	default:
		target = path
	}
	ok, err := doublestar.Match(r.pattern, target)
	if err != nil {
		// Malformed pattern — treat as non-matching rather than
		// panicking mid-transfer. Parse-time validation catches most
		// real mistakes before we get here.
		return false
	}
	return ok
}
