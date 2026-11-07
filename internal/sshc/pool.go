package sshc

import (
	"context"
	"sync"
	"time"
)

// Pool shares one Client per config file and host, as ssh's ControlMaster
// with ControlPersist 60 would: Get counts users, a Client lingers Linger
// after its last release, and a dead one is dropped so the next Get dials
// again. The zero Pool is ready.
type Pool struct {
	Linger time.Duration // 0 = 60 s

	mu sync.Mutex
	m  map[string]*entry
}

type entry struct {
	ready chan struct{} // closed once dialed
	c     *Client
	err   error
	refs  int
	timer *time.Timer
}

// Get is host's shared client and the release that hands it back.
// Concurrent Gets for one host share its dial. ponytail: that dial runs
// under the first caller's ctx, and takes its Options; key on the options
// too if callers start to differ.
func (p *Pool) Get(ctx context.Context, host string, o Options) (*Client, func(), error) {
	key := o.File + "\x00" + host
	p.mu.Lock()
	if p.m == nil {
		p.m = map[string]*entry{}
	}
	e := p.m[key]
	if e != nil && e.c != nil && e.c.Err() != nil {
		e = nil
	}
	first := e == nil
	if first {
		e = &entry{ready: make(chan struct{})}
		p.m[key] = e
	}
	e.refs++
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	p.mu.Unlock()

	var once sync.Once
	release := func() { once.Do(func() { p.release(key, e) }) }
	if first {
		c, err := Dial(ctx, host, o)
		p.mu.Lock()
		e.c, e.err = c, err
		if err != nil && p.m[key] == e {
			delete(p.m, key)
		}
		p.mu.Unlock()
		close(e.ready)
	}
	select {
	case <-e.ready:
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	}
	if e.err != nil {
		return nil, nil, e.err
	}
	return e.c, release, nil
}

func (p *Pool) release(key string, e *entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.refs--; e.refs > 0 || e.c == nil {
		return
	}
	linger := p.Linger
	if linger == 0 {
		linger = 60 * time.Second
	}
	// The refs check inside stands for a Get that raced the timer firing
	// (Stop too late); only then does a released entry have users again.
	e.timer = time.AfterFunc(linger, func() {
		p.mu.Lock()
		idle := e.refs == 0
		if idle && p.m[key] == e {
			delete(p.m, key)
		}
		p.mu.Unlock()
		if idle {
			e.c.Close()
		}
	})
}
