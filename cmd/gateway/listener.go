package main

import (
	"net"
	"sync"
)

// limitListener caps concurrent connections (security #17): Accept waits
// while n connections are open. Idle keep-alive connections count too; they
// close after LGAI_IDLE_TIMEOUT.
type limitListener struct {
	net.Listener
	sem  chan struct{}
	done chan struct{}
	once sync.Once
}

func limitListen(l net.Listener, n int) net.Listener {
	return &limitListener{Listener: l, sem: make(chan struct{}, max(n, 1)), done: make(chan struct{})}
}

func (l *limitListener) Accept() (net.Conn, error) {
	select {
	case l.sem <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &limitConn{Conn: c, release: func() { <-l.sem }}, nil
}

func (l *limitListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

type limitConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
