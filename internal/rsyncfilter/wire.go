package rsyncfilter

import (
	"io"

	"github.com/gokrazy/rsync/internal/rsyncwire"
)

// Send writes l to c in rsync wire format: a sequence of length-prefixed
// canonical rule texts terminated by a zero-length marker. A nil List
// is treated as empty (just the terminator).
//
// exclude.c:send_filter_list
func Send(c *rsyncwire.Conn, l *List) error {
	if l != nil {
		for _, r := range l.rules {
			text := r.canonical()
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

// Recv reads a filter list from c. The returned List is never nil, even
// when the peer sent only the terminator.
//
// exclude.c:recv_filter_list
func Recv(c *rsyncwire.Conn) (*List, error) {
	l := &List{}
	const exclusionListEnd = 0
	for {
		length, err := c.ReadInt32()
		if err != nil {
			return nil, err
		}
		if length == exclusionListEnd {
			return l, nil
		}
		line := make([]byte, length)
		if _, err := io.ReadFull(c.Reader, line); err != nil {
			return nil, err
		}
		if err := l.Parse(string(line)); err != nil {
			return nil, err
		}
	}
}
