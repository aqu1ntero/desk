package infra

import (
	"context"
	"slices"
	"time"
)

// Fetching a server's password (alt+p, for a sudo prompt) can take a while:
// the vault's server answers, or it asks for its master password first. The
// pane shows each step in a box like the connection's, so the wait says what
// it waits on. It goes the moment the password is typed; a failed one stays
// with its error until hidden or retried.

// pwFetch is a pane's password fetch: its steps, when it ended (zero while
// it runs) and whether the user hid its box.
type pwFetch struct {
	steps  []connStep
	end    time.Time
	hidden bool
}

// fetchStart begins a fetch, replacing the last one.
func (t *Term) fetchStart() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.fetch = &pwFetch{}
	t.mu.Unlock()
	t.redraw()
}

// fetchStep ends the fetch's running step and starts name; user marks a
// wait on the user (the vault's master password).
func (t *Term) fetchStep(name string, user bool) {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	if f := t.fetch; f != nil && f.end.IsZero() {
		if n := len(f.steps); n > 0 && f.steps[n-1].End.IsZero() {
			f.steps[n-1].End = now
		}
		f.steps = append(f.steps, connStep{Name: name, User: user, Start: now})
	}
	t.mu.Unlock()
	t.redraw()
}

// fetchEnd ends the fetch, its running step failed with err when there is one.
func (t *Term) fetchEnd(err error) {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	if f := t.fetch; f != nil && f.end.IsZero() {
		if n := len(f.steps); n > 0 && f.steps[n-1].End.IsZero() {
			f.steps[n-1].End = now
		}
		if err != nil {
			if len(f.steps) == 0 {
				f.steps = append(f.steps, connStep{Name: "Getting the password", Start: now, End: now})
			}
			f.steps[len(f.steps)-1].Err = err.Error()
		}
		f.end = now
	}
	t.mu.Unlock()
	t.redraw()
}

// fetchHide takes the fetch's box away; a running fetch goes on.
func (t *Term) fetchHide() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.fetch != nil {
		t.fetch.hidden = true
	}
	t.mu.Unlock()
}

// Fetch is the fetch's box: its steps, and whether it shows: while it runs
// and once it failed, never after it went well.
func (t *Term) Fetch() (ss []connStep, shown bool) {
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.fetch
	if f == nil || f.hidden || !f.end.IsZero() && !fetchFailed(f.steps) {
		return nil, false
	}
	return slices.Clone(f.steps), true
}

// fetchFailed says the fetch's last step failed.
func fetchFailed(ss []connStep) bool { return len(ss) > 0 && ss[len(ss)-1].Err != "" }

// redraw asks the window for a frame (notify), when there is one.
func (t *Term) redraw() {
	if t.notify != nil {
		t.notify()
	}
}

// stepKey carries a fetch's step function through a context, down to the
// vault code that knows when it waits on the user.
type stepKey struct{}

// withStep is ctx carrying step.
func withStep(ctx context.Context, step func(name string, user bool)) context.Context {
	return context.WithValue(ctx, stepKey{}, step)
}

// stepOf is ctx's step function, one doing nothing without one.
func stepOf(ctx context.Context) func(name string, user bool) {
	if f, ok := ctx.Value(stepKey{}).(func(string, bool)); ok && f != nil {
		return f
	}
	return func(string, bool) {}
}
