package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

type Config struct {
	ClaudeBin string
	// Dir is the working directory every claude process runs in. Claude Code
	// files transcripts by cwd, so --resume only finds sessions started here.
	Dir string
	// SpareTTL is how long a spare may wait before it is replaced, so that
	// an updated Claude Code reaches the next caller without a restart.
	SpareTTL time.Duration
	// IdleTTL is how long a used session stays live after its last turn,
	// unless its last request asked for a different keep-alive.
	IdleTTL time.Duration
	// MaxLive caps how many used sessions are kept live at once. Prioritized
	// sessions are never evicted to make room, so they can exceed it.
	MaxLive int
	// ProfileIdleTTL is how long a spare for a non-default profile is kept
	// after the last request that asked for that profile.
	ProfileIdleTTL time.Duration
	StartTimeout   time.Duration
	TurnTimeout    time.Duration
	TickInterval   time.Duration
	// RetryBackoff is the wait before respawning a spare that failed to start.
	RetryBackoff time.Duration
	// Defaults are the profiles that always have a spare.
	Defaults []Profile
	// OnResult is called with every completed turn, for cost accounting.
	OnResult func(*AskResponse, string)

	// CompactAfter is how long after an --auto-compact turn its session is
	// compacted: just inside the prompt cache's hour, so the compaction reads
	// a warm cache.
	CompactAfter time.Duration
	// CompactLatest is the latest point after the last turn at which a due
	// compaction still runs. Past it the cache may have expired, and
	// compacting would pay for a cold read of the whole context.
	CompactLatest time.Duration
	// StateFile persists scheduled compactions across restarts; "" keeps
	// them in memory only.
	StateFile string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// MaxKeepAlive caps a request's keep-alive at the prompt cache's lifetime:
// past it, a live process saves only Claude Code's startup.
const MaxKeepAlive = time.Hour

type AskRequest struct {
	Prompt    string  `json:"prompt"`
	SessionID string  `json:"session_id,omitempty"`
	Profile   Profile `json:"profile"`
	// Tag names the caller in the usage log, e.g. "summary-chat".
	Tag string `json:"tag,omitempty"`

	// The options below apply to the session from this request until the
	// next one, which replaces them; a request that leaves one out turns it off.

	// KeepAlive keeps the session's process live this long after the turn,
	// in place of IdleTTL. Zero means IdleTTL; capped at MaxKeepAlive.
	KeepAlive time.Duration `json:"keep_alive,omitempty"`
	// Priority exempts the session from eviction under MaxLive.
	Priority bool `json:"priority,omitempty"`
	// AutoCompact compacts the session CompactAfter after this turn, unless
	// another request arrives first.
	AutoCompact bool `json:"auto_compact,omitempty"`
	// CompactAbove compacts the session right after this turn, in the
	// background, if its context exceeds this many tokens. Zero is off.
	CompactAbove int `json:"compact_above,omitempty"`
}

// Source says how the turn got its process, which is what decides latency.
type Source string

const (
	SourceSpare  Source = "spare"  // a pre-started process for this profile
	SourceLive   Source = "live"   // the session's own process, kept from its last turn
	SourceCold   Source = "cold"   // a new session that found no spare
	SourceResume Source = "resume" // an existing session whose process had been shut down
)

type AskResponse struct {
	SessionID     string          `json:"session_id"`
	Source        Source          `json:"source"`
	Model         string          `json:"model"`
	Result        string          `json:"result"`
	IsError       bool            `json:"is_error"`
	Subtype       string          `json:"subtype"`
	TotalCostUSD  float64         `json:"total_cost_usd"`
	DurationMS    int64           `json:"duration_ms"`
	DurationAPIMS int64           `json:"duration_api_ms"`
	WallMS        int64           `json:"wall_ms"`
	Usage         json.RawMessage `json:"usage,omitempty"`
	// ContextTokens is the session's context size after the turn: the final
	// API call's input, cache read and cache creation tokens.
	ContextTokens int `json:"context_tokens"`
	// Compaction reports a compaction of this session that finished since
	// its previous turn, or failed.
	Compaction *CompactionInfo `json:"compaction,omitempty"`
	// NextCompaction is the compaction this turn scheduled or started.
	NextCompaction *NextCompaction `json:"next_compaction,omitempty"`
}

var ErrBusy = errors.New("session already has a turn in progress")

type slot struct {
	profile Profile
	pinned  bool
	// current is the spare a request will claim; it may still be starting.
	current *Proc
	// replacing is a newer spare starting up to retire current once ready.
	replacing     *Proc
	lastRequested time.Time
	retryAt       time.Time
}

type liveEntry struct {
	proc     *Proc
	lastUsed time.Time
	ttl      time.Duration
	priority bool
}

type Pool struct {
	cfg Config

	mu    sync.Mutex
	slots map[string]*slot
	live  map[string]*liveEntry
	// busy holds sessions with a turn in flight, live or not.
	busy map[string]bool
	// pending holds scheduled compactions; running, compactions under way,
	// which a request for the session waits out; compacted, finished ones
	// not yet reported to a turn.
	pending   map[string]*compactJob
	running   map[string]*runningCompaction
	compacted map[string]*CompactionInfo
	closed    bool
	stop      chan struct{}
}

func (p *Pool) now() time.Time {
	if p.cfg.Now != nil {
		return p.cfg.Now()
	}
	return time.Now()
}

func NewPool(cfg Config) *Pool {
	p := &Pool{
		cfg:   cfg,
		slots: map[string]*slot{},
		live:  map[string]*liveEntry{},
		busy:  map[string]bool{},

		pending:   map[string]*compactJob{},
		running:   map[string]*runningCompaction{},
		compacted: map[string]*CompactionInfo{},
		stop:      make(chan struct{}),
	}
	p.mu.Lock()
	for _, prof := range cfg.Defaults {
		s := p.slotLocked(prof)
		s.pinned = true
		p.fillLocked(s)
	}
	p.restorePendingLocked()
	p.mu.Unlock()
	go p.loop()
	return p
}

func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (p *Pool) spawn(prof Profile, sessionID string, resume bool) (*Proc, error) {
	return spawn(spawnOpts{
		ClaudeBin: p.cfg.ClaudeBin,
		Dir:       p.cfg.Dir,
		Profile:   prof,
		SessionID: sessionID,
		Resume:    resume,
	})
}

func (p *Pool) slotLocked(prof Profile) *slot {
	k := prof.key()
	s := p.slots[k]
	if s == nil {
		s = &slot{profile: prof}
		p.slots[k] = s
	}
	return s
}

// fillLocked gives an empty slot a spare: the replacement already starting,
// if there is one, otherwise a fresh process.
func (p *Pool) fillLocked(s *slot) {
	if s.current != nil || p.closed {
		return
	}
	if s.replacing != nil {
		s.current, s.replacing = s.replacing, nil
		return
	}
	if time.Now().Before(s.retryAt) {
		return
	}
	proc, err := p.spawn(s.profile, newSessionID(), false)
	if err != nil {
		log.Printf("spare %s: spawn failed: %v", s.profile.Model, err)
		s.retryAt = time.Now().Add(p.cfg.RetryBackoff)
		return
	}
	s.current = proc
	go p.watchSpare(s, proc)
}

// startReplacementLocked starts a newer spare for a slot whose current one
// has aged out. The old spare keeps serving until the new one is ready.
func (p *Pool) startReplacementLocked(s *slot) {
	proc, err := p.spawn(s.profile, newSessionID(), false)
	if err != nil {
		log.Printf("spare %s: replacement spawn failed: %v", s.profile.Model, err)
		s.retryAt = time.Now().Add(p.cfg.RetryBackoff)
		return
	}
	s.replacing = proc
	go p.watchSpare(s, proc)
}

// watchSpare settles a spare once it is ready or has failed. A replacement
// that is still the slot's replacement when it becomes ready retires the
// current spare. If a request claimed the current spare in the meantime,
// fillLocked has already promoted the replacement and there is nothing to do.
func (p *Pool) watchSpare(s *slot, proc *Proc) {
	err := proc.WaitReady(p.cfg.StartTimeout)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		log.Printf("spare %s: failed to start: %v", s.profile.Model, err)
		proc.Kill()
		if s.current == proc {
			s.current = nil
		}
		if s.replacing == proc {
			s.replacing = nil
		}
		s.retryAt = time.Now().Add(p.cfg.RetryBackoff)
		return
	}
	if s.replacing == proc {
		old := s.current
		s.current, s.replacing = proc, nil
		if old != nil {
			old.Kill()
		}
	}
}

func (p *Pool) Ask(req AskRequest) (*AskResponse, error) {
	start := time.Now()
	if req.KeepAlive > MaxKeepAlive {
		req.KeepAlive = MaxKeepAlive
	}
	var proc *Proc
	var source Source
	var waited time.Duration
	var err error
	if req.SessionID == "" {
		proc, source, err = p.acquireNew(req.Profile)
	} else {
		proc, source, waited, err = p.acquireExisting(req.SessionID, req.Profile)
	}
	if err != nil {
		return nil, err
	}
	sessionID := proc.SessionID
	// finish releases the session itself on success, in the same critical
	// section that may start a compaction, so no request slips in between.
	released := false
	defer func() {
		if !released {
			p.mu.Lock()
			delete(p.busy, sessionID)
			p.mu.Unlock()
		}
	}()

	if err := proc.WaitReady(p.cfg.StartTimeout); err != nil {
		proc.Kill()
		if source != SourceSpare {
			return nil, fmt.Errorf("session %s (%s): starting claude: %w", sessionID, source, err)
		}
		// A spare that died while waiting is not the caller's problem.
		log.Printf("claimed spare %s failed (%v); starting a cold process", sessionID, err)
		p.mu.Lock()
		delete(p.busy, sessionID)
		p.mu.Unlock()
		if proc, err = p.spawn(req.Profile, newSessionID(), false); err != nil {
			return nil, err
		}
		source, sessionID = SourceCold, proc.SessionID
		p.mu.Lock()
		p.busy[sessionID] = true
		p.mu.Unlock()
		if err := proc.WaitReady(p.cfg.StartTimeout); err != nil {
			proc.Kill()
			return nil, fmt.Errorf("session %s (%s): starting claude: %w", sessionID, source, err)
		}
	}
	turnStart := p.now()
	tr, err := proc.Turn(req.Prompt, p.cfg.TurnTimeout)
	if err != nil {
		proc.Kill()
		return nil, fmt.Errorf("session %s (%s): %w", sessionID, source, err)
	}
	resp := &AskResponse{Source: source, Model: tr.Model, WallMS: time.Since(start).Milliseconds(), ContextTokens: tr.ContextTokens}
	var r struct {
		SessionID     string          `json:"session_id"`
		Result        string          `json:"result"`
		IsError       bool            `json:"is_error"`
		Subtype       string          `json:"subtype"`
		TotalCostUSD  float64         `json:"total_cost_usd"`
		DurationMS    int64           `json:"duration_ms"`
		DurationAPIMS int64           `json:"duration_api_ms"`
		Usage         json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(tr.Raw, &r); err != nil {
		proc.Kill()
		return nil, fmt.Errorf("session %s (%s): parsing claude's result: %w", sessionID, source, err)
	}
	resp.SessionID = r.SessionID
	if resp.SessionID == "" {
		resp.SessionID = sessionID
	}
	resp.Result, resp.IsError, resp.Subtype = r.Result, r.IsError, r.Subtype
	resp.TotalCostUSD, resp.DurationMS, resp.DurationAPIMS, resp.Usage = r.TotalCostUSD, r.DurationMS, r.DurationAPIMS, r.Usage

	p.mu.Lock()
	p.keepLiveLocked(proc, resp.SessionID, req)
	if info := p.compacted[resp.SessionID]; info != nil {
		delete(p.compacted, resp.SessionID)
		info.CompactedBeforeMS = turnStart.Sub(info.finished).Milliseconds()
		info.WaitedMS = waited.Milliseconds()
		resp.Compaction = info
	}
	resp.NextCompaction = p.afterTurnLocked(resp.SessionID, proc, req, tr.ContextTokens)
	delete(p.busy, sessionID)
	delete(p.busy, resp.SessionID)
	released = true
	p.mu.Unlock()

	if p.cfg.OnResult != nil {
		p.cfg.OnResult(resp, req.Tag)
	}
	return resp, nil
}

func (p *Pool) acquireNew(prof Profile) (*Proc, Source, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, "", errors.New("daemon is shutting down")
	}
	s := p.slotLocked(prof)
	s.lastRequested = time.Now()
	proc := s.current
	if proc != nil && proc.Alive() {
		s.current = nil
	} else {
		s.current, proc = nil, nil
	}
	p.fillLocked(s)
	if proc != nil {
		p.busy[proc.SessionID] = true
		p.mu.Unlock()
		return proc, SourceSpare, nil
	}
	p.mu.Unlock()

	proc, err := p.spawn(prof, newSessionID(), false)
	if err != nil {
		return nil, "", err
	}
	p.mu.Lock()
	p.busy[proc.SessionID] = true
	p.mu.Unlock()
	return proc, SourceCold, nil
}

// acquireExisting waits out a compaction of the session, if one is running,
// and reports how long it waited.
func (p *Pool) acquireExisting(sessionID string, prof Profile) (*Proc, Source, time.Duration, error) {
	var waited time.Duration
	p.mu.Lock()
	for {
		if p.closed {
			p.mu.Unlock()
			return nil, "", waited, errors.New("daemon is shutting down")
		}
		rc := p.running[sessionID]
		if rc == nil {
			break
		}
		p.mu.Unlock()
		t0 := time.Now()
		<-rc.done
		waited += time.Since(t0)
		p.mu.Lock()
	}
	if p.busy[sessionID] {
		p.mu.Unlock()
		return nil, "", waited, ErrBusy
	}
	p.busy[sessionID] = true
	e := p.live[sessionID]
	delete(p.live, sessionID)
	p.mu.Unlock()

	if e != nil && e.proc.Alive() {
		proc := e.proc
		// Effort and thinking are process flags, so changing them means a
		// new process. The model can be switched in place. The system
		// prompt is ignored: Claude Code keeps the one the session began with.
		if proc.Profile.Effort == prof.Effort && proc.Profile.NoThinking == prof.NoThinking {
			if prof.Model == "" || proc.Profile.Model == prof.Model {
				return proc, SourceLive, waited, nil
			}
			if err := proc.SetModel(prof.Model, p.cfg.StartTimeout); err == nil {
				return proc, SourceLive, waited, nil
			}
		}
		proc.Kill()
	}
	proc, err := p.spawn(prof, sessionID, true)
	if err != nil {
		p.mu.Lock()
		delete(p.busy, sessionID)
		p.mu.Unlock()
		return nil, "", waited, err
	}
	return proc, SourceResume, waited, nil
}

func (p *Pool) keepLiveLocked(proc *Proc, sessionID string, req AskRequest) {
	if p.closed || !proc.Alive() {
		proc.Kill()
		return
	}
	ttl := req.KeepAlive
	if ttl <= 0 {
		ttl = p.cfg.IdleTTL
	}
	proc.SessionID = sessionID
	p.live[sessionID] = &liveEntry{proc: proc, lastUsed: p.now(), ttl: ttl, priority: req.Priority}
	p.evictLocked()
}

// evictLocked shuts down the least recently used sessions until MaxLive is
// met, never a prioritized one: if only those are left, the cap gives way.
func (p *Pool) evictLocked() {
	for len(p.live) > p.cfg.MaxLive {
		var oldestID string
		var oldest time.Time
		for id, e := range p.live {
			if !e.priority && (oldestID == "" || e.lastUsed.Before(oldest)) {
				oldestID, oldest = id, e.lastUsed
			}
		}
		if oldestID == "" {
			return
		}
		p.live[oldestID].proc.Kill()
		delete(p.live, oldestID)
	}
}

func (p *Pool) loop() {
	t := time.NewTicker(p.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			p.tick()
		case <-p.stop:
			return
		}
	}
}

func (p *Pool) tick() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	now := time.Now()
	clock := p.now()
	for k, s := range p.slots {
		if !s.pinned && now.Sub(s.lastRequested) > p.cfg.ProfileIdleTTL {
			for _, proc := range []*Proc{s.current, s.replacing} {
				if proc != nil {
					proc.Kill()
				}
			}
			delete(p.slots, k)
			continue
		}
		if s.current != nil && !s.current.Alive() {
			s.current = nil
		}
		if s.current == nil {
			p.fillLocked(s)
			continue
		}
		if s.replacing == nil && s.current.IsReady() && now.Sub(s.current.Created) > p.cfg.SpareTTL && !now.Before(s.retryAt) {
			p.startReplacementLocked(s)
		}
	}
	for id, e := range p.live {
		if !e.proc.Alive() || clock.Sub(e.lastUsed) > e.ttl {
			e.proc.Kill()
			delete(p.live, id)
		}
	}
	p.runDueCompactionsLocked(clock)
}

func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	close(p.stop)
	for _, s := range p.slots {
		for _, proc := range []*Proc{s.current, s.replacing} {
			if proc != nil {
				proc.Kill()
			}
		}
	}
	for _, e := range p.live {
		e.proc.Kill()
	}
	for _, rc := range p.running {
		rc.kill()
	}
}

type SpareStatus struct {
	Profile   Profile `json:"profile"`
	Pinned    bool    `json:"pinned"`
	Ready     bool    `json:"ready"`
	AgeS      int64   `json:"age_s"`
	Replacing bool    `json:"replacing"`
}

type LiveStatus struct {
	SessionID  string  `json:"session_id"`
	Profile    Profile `json:"profile"`
	IdleS      int64   `json:"idle_s"`
	KeepAliveS int64   `json:"keep_alive_s"`
	Priority   bool    `json:"priority,omitempty"`
}

type Status struct {
	Spares      []SpareStatus      `json:"spares"`
	Live        []LiveStatus       `json:"live"`
	Busy        []string           `json:"busy"`
	Compactions []CompactionStatus `json:"compactions"`
}

func (p *Pool) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	clock := p.now()
	st := Status{Spares: []SpareStatus{}, Live: []LiveStatus{}, Busy: []string{}, Compactions: p.compactionStatusLocked(clock)}
	for _, s := range p.slots {
		ss := SpareStatus{Profile: s.profile, Pinned: s.pinned, Replacing: s.replacing != nil}
		if s.current != nil {
			ss.Ready = s.current.IsReady()
			ss.AgeS = int64(now.Sub(s.current.Created).Seconds())
		}
		st.Spares = append(st.Spares, ss)
	}
	for id, e := range p.live {
		st.Live = append(st.Live, LiveStatus{
			SessionID: id, Profile: e.proc.Profile, IdleS: int64(clock.Sub(e.lastUsed).Seconds()),
			KeepAliveS: int64(e.ttl.Seconds()), Priority: e.priority,
		})
	}
	for id := range p.busy {
		st.Busy = append(st.Busy, id)
	}
	sort.Slice(st.Spares, func(i, j int) bool { return st.Spares[i].Profile.key() < st.Spares[j].Profile.key() })
	sort.Slice(st.Live, func(i, j int) bool { return st.Live[i].IdleS < st.Live[j].IdleS })
	sort.Strings(st.Busy)
	return st
}
