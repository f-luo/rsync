package rsyncfilter

import "strings"

// wildmatch reports whether pattern matches text under rsync shell-glob
// semantics: '?' and '*' stop at '/', '**' crosses '/', '[...]' classes
// (with '!'/'^' negation and 'a-z' ranges), and '\c' escapes a literal.
//
// wildmatch.c:dowild
func wildmatch(p, t string) bool {
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
			stars := 0
			for len(p) > 0 && p[0] == '*' {
				stars++
				p = p[1:]
			}
			crossSlash := stars >= 2
			if len(p) == 0 {
				return crossSlash || strings.IndexByte(t, '/') < 0
			}
			// '**/' may match zero path segments: try the tail past
			// the '/' at the current text position. Mirrors
			// wildmatch.c's zero-segment collapse.
			if crossSlash && p[0] == '/' && wildmatch(p[1:], t) {
				return true
			}
			for i := 0; ; i++ {
				if wildmatch(p, t[i:]) {
					return true
				}
				if i >= len(t) || (!crossSlash && t[i] == '/') {
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
func matchClass(p string, c byte) (matched bool, size int) {
	if len(p) < 2 || p[0] != '[' {
		return false, 0
	}
	i := 1
	neg := false
	if p[i] == '!' || p[i] == '^' {
		neg = true
		i++
	}
	first := true
	for i < len(p) {
		if !first && p[i] == ']' {
			if neg {
				matched = !matched
			}
			return matched, i + 1
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
