package rsyncfilter

import (
	"fmt"
	"io"
	"strings"

	"github.com/gokrazy/rsync/internal/rsyncwire"
)

// Recv reads a filter rule list from c in rsync's on-the-wire format:
// repeated (int32 length, length bytes of rule text) pairs, terminated
// by a zero-length entry. Mirrors exclude.c:recv_filter_list.
func Recv(c *rsyncwire.Conn) (*List, error) {
	l := New()
	for {
		length, err := c.ReadInt32()
		if err != nil {
			return nil, err
		}
		if length == 0 {
			return l, nil
		}
		if length < 0 {
			return nil, fmt.Errorf("rsyncfilter.Recv: negative length %d", length)
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(c.Reader, buf); err != nil {
			return nil, err
		}
		r, err := Parse(string(buf))
		if err != nil {
			return nil, err
		}
		l.Add(r)
	}
}

// Send writes l to c in rsync's on-the-wire format, terminated by a
// zero-length entry. Each rule is serialised via Rule.Canonical so
// that a receiver's Parse reconstructs the same flag set.
func Send(c *rsyncwire.Conn, l *List) error {
	if l != nil {
		for _, r := range l.Filters {
			text := r.Canonical()
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

// Canonical returns the rule's on-the-wire textual form — the inverse
// of Parse. The returned string always carries an explicit "- "/"+ "
// sign (or is exactly "!" for the clear-list rule), so a peer that
// re-parses it recovers the same flags.
func (r *Rule) Canonical() string {
	if r.flag&filtruleClearList != 0 {
		return "!"
	}
	var b strings.Builder
	if r.flag&filtruleInclude != 0 {
		b.WriteString("+ ")
	} else {
		b.WriteString("- ")
	}
	if r.flag&filtruleAnchored != 0 {
		b.WriteByte('/')
	}
	b.WriteString(r.pattern)
	if r.flag&filtruleDirectory != 0 {
		b.WriteByte('/')
	}
	return b.String()
}
