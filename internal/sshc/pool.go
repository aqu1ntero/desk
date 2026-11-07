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
	ready  chan struct{} // closed once dialed
	cancel func()        // stops the dial
	c      *Client
	err    error
	refs   int
	timer  *time.Timer
}

// Get is host's shared client and the release that hands it back.
// Concurrent Gets for one host share its dial, which belongs to none of
// them: each waits under its own ctx, and the dial is cancelled only once
// every waiter has gone. ponytail: the dial takes the first caller's
// Options (its Prompt too); key on the options if callers start to differ.
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
	if e == nil {
		dctx, cancel := context.WithCancel(context.Background())
		e = &entry{ready: make(chan struct{}), cancel: cancel}
		p.m[key] = e
		go p.dial(dctx, key, e, host, o)
	}
	e.refs++
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	p.mu.Unlock()

	var once sync.Once
	release := func() { once.Do(func() { p.release(key, e) }) }
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

// dial is an entry's shared dial. A client that arrives after its last
// waiter left is closed: nobody holds it to release it.
func (p *Pool) dial(ctx context.Context, key string, e *entry, host string, o Options) {
	c, err := Dial(ctx, host, o)
	e.cancel()
	p.mu.Lock()
	e.c, e.err = c, err
	orphan := err == nil && e.refs == 0
	if (err != nil || orphan) && p.m[key] == e {
		delete(p.m, key)
	}
	p.mu.Unlock()
	close(e.ready)
	if orphan {
		c.Close()
	}
}

func (p *Pool) release(key string, e *entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.refs--; e.refs > 0 {
		return
	}
	if e.c == nil {
		// Still dialing with nobody waiting: stop it, and let the next Get
		// dial afresh rather than join a cancelled dial.
		if p.m[key] == e {
			delete(p.m, key)
		}
		e.cancel()
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
