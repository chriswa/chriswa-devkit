package main

import "time"

// An abort stops a turn where it is. It is not a rewind: once the prompt has
// been sent, it stays in the session, followed by Claude Code's
// "[Request interrupted by user]" and whatever of the reply was written
// before the abort, and the session's next turn comes after all of that. A
// turn aborted before its prompt was sent leaves the session as it was.
//
// A caller names a turn with AskRequest.TurnID and aborts it by that name,
// since a new session has no ID until its turn returns.

// AbortInfo is set on the response of a turn that was aborted.
type AbortInfo struct {
	// PromptInSession says whether the prompt reached the session. True: the
	// session holds it and the interrupted reply, and the caller must not
	// resend it as though it were never seen. False: the session never saw it.
	PromptInSession bool `json:"prompt_in_session"`
}

// abortTombstoneTTL is how long an abort for a turn not yet seen is kept: an
// abort can reach the daemon before the request it names.
const abortTombstoneTTL = time.Minute

// turnHandle is one named turn's abort signal.
type turnHandle struct {
	abort   chan struct{}
	aborted bool
	// early is set on an abort that arrived before its turn, until the turn claims it.
	early time.Time
}

// Abort stops the named turn, or the turn of that name still to arrive.
// Reports whether a turn of that name was running.
func (p *Pool) Abort(turnID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.turns[turnID]
	if h == nil {
		p.turns[turnID] = &turnHandle{abort: closedChan(), aborted: true, early: time.Now()}
		return false
	}
	if !h.aborted {
		h.aborted = true
		close(h.abort)
	}
	return h.early.IsZero()
}

// claimTurn registers a named turn, returning its abort signal. A turn whose
// abort arrived first gets a signal that has already fired.
func (p *Pool) claimTurnLocked(turnID string) <-chan struct{} {
	if turnID == "" {
		return nil
	}
	if h := p.turns[turnID]; h != nil {
		h.early = time.Time{}
		return h.abort
	}
	h := &turnHandle{abort: make(chan struct{})}
	p.turns[turnID] = h
	return h.abort
}

func (p *Pool) releaseTurn(turnID string) {
	if turnID == "" {
		return
	}
	p.mu.Lock()
	delete(p.turns, turnID)
	p.mu.Unlock()
}

// expireAbortsLocked drops aborts whose turn never came.
func (p *Pool) expireAbortsLocked(now time.Time) {
	for id, h := range p.turns {
		if !h.early.IsZero() && now.Sub(h.early) > abortTombstoneTTL {
			delete(p.turns, id)
		}
	}
}

func fired(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
