package listener

import (
	"errors"
	"net"
	"os"
	"sync/atomic"
	"time"
)

// idleConn arms a read deadline before every read so a connection that sends
// nothing for the idle timeout is closed. While the listener drains it leaves
// the drain's deadline (now) alone, so draining still ends reads at once.
type idleConn struct {
	net.Conn
	timeout time.Duration
	l       *Listener
	idle    atomic.Bool // the last read ended on the idle deadline
}

func (c *idleConn) Read(p []byte) (int, error) {
	if !c.l.isDraining() {
		_ = c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	}
	n, err := c.Conn.Read(p)
	if err != nil && errors.Is(err, os.ErrDeadlineExceeded) && !c.l.isDraining() {
		c.idle.Store(true)
	}
	return n, err
}

func (l *Listener) isDraining() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.draining
}
