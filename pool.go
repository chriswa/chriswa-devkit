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
	// IdleTTL is how long a used session stays live after its last turn.
	IdleTTL time.Duration
	// MaxLive caps how many used sessions are kept live at once.
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
}

type AskRequest struct {
	Prompt    string  `json:"prompt"`
	SessionID string  `json:"session_id,omitempty"`
	Profile   Profile `json:"profile"`
	// Tag names the caller in the usage log, e.g. "summary-chat".
	Tag string `json:"tag,omitempty"`
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
}

type Pool struct {
	cfg Config

	mu    sync.Mutex
	slots map[string]*slot
	live  map[string]*liveEntry
	// busy holds sessions with a turn in flight, live or not.
	busy   map[string]bool
	closed bool
	stop   chan struct{}
}

func NewPool(cfg Config) *Pool {
	p := &Pool{
		cfg:   cfg,
		slots: map[string]*slot{},
		live:  map[string]*liveEntry{},
		busy:  map[string]bool{},
		stop:  make(chan struct{}),
	}
	p.mu.Lock()
	for _, prof := range cfg.Defaults {
		s := p.slotLocked(prof)
		s.pinned = true
		p.fillLocked(s)
	}
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
	var proc *Proc
	var source Source
	var err error
	if req.SessionID == "" {
		proc, source, err = p.acquireNew(req.Profile)
	} else {
		proc, source, err = p.acquireExisting(req.SessionID, req.Profile)
	}
	if err != nil {
		return nil, err
	}
	sessionID := proc.SessionID
	defer func() {
		p.mu.Lock()
		delete(p.busy, sessionID)
		p.mu.Unlock()
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
	tr, err := proc.Turn(req.Prompt, p.cfg.TurnTimeout)
	if err != nil {
		proc.Kill()
		return nil, fmt.Errorf("session %s (%s): %w", sessionID, source, err)
	}
	resp := &AskResponse{Source: source, Model: tr.Model, WallMS: time.Since(start).Milliseconds()}
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

	p.keepLive(proc, resp.SessionID)
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

func (p *Pool) acquireExisting(sessionID string, prof Profile) (*Proc, Source, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, "", errors.New("daemon is shutting down")
	}
	if p.busy[sessionID] {
		p.mu.Unlock()
		return nil, "", ErrBusy
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
				return proc, SourceLive, nil
			}
			if err := proc.SetModel(prof.Model, p.cfg.StartTimeout); err == nil {
				return proc, SourceLive, nil
			}
		}
		proc.Kill()
	}
	proc, err := p.spawn(prof, sessionID, true)
	if err != nil {
		p.mu.Lock()
		delete(p.busy, sessionID)
		p.mu.Unlock()
		return nil, "", err
	}
	return proc, SourceResume, nil
}

func (p *Pool) keepLive(proc *Proc, sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || !proc.Alive() {
		proc.Kill()
		return
	}
	proc.SessionID = sessionID
	p.live[sessionID] = &liveEntry{proc: proc, lastUsed: time.Now()}
	for len(p.live) > p.cfg.MaxLive {
		var oldestID string
		var oldest time.Time
		for id, e := range p.live {
			if oldestID == "" || e.lastUsed.Before(oldest) {
				oldestID, oldest = id, e.lastUsed
			}
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
		if !e.proc.Alive() || now.Sub(e.lastUsed) > p.cfg.IdleTTL {
			e.proc.Kill()
			delete(p.live, id)
		}
	}
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
}

type SpareStatus struct {
	Profile   Profile `json:"profile"`
	Pinned    bool    `json:"pinned"`
	Ready     bool    `json:"ready"`
	AgeS      int64   `json:"age_s"`
	Replacing bool    `json:"replacing"`
}

type LiveStatus struct {
	SessionID string  `json:"session_id"`
	Profile   Profile `json:"profile"`
	IdleS     int64   `json:"idle_s"`
}

type Status struct {
	Spares []SpareStatus `json:"spares"`
	Live   []LiveStatus  `json:"live"`
	Busy   []string      `json:"busy"`
}

func (p *Pool) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	st := Status{Spares: []SpareStatus{}, Live: []LiveStatus{}, Busy: []string{}}
	for _, s := range p.slots {
		ss := SpareStatus{Profile: s.profile, Pinned: s.pinned, Replacing: s.replacing != nil}
		if s.current != nil {
			ss.Ready = s.current.IsReady()
			ss.AgeS = int64(now.Sub(s.current.Created).Seconds())
		}
		st.Spares = append(st.Spares, ss)
	}
	for id, e := range p.live {
		st.Live = append(st.Live, LiveStatus{SessionID: id, Profile: e.proc.Profile, IdleS: int64(now.Sub(e.lastUsed).Seconds())})
	}
	for id := range p.busy {
		st.Busy = append(st.Busy, id)
	}
	sort.Slice(st.Spares, func(i, j int) bool { return st.Spares[i].Profile.key() < st.Spares[j].Profile.key() })
	sort.Slice(st.Live, func(i, j int) bool { return st.Live[i].IdleS < st.Live[j].IdleS })
	sort.Strings(st.Busy)
	return st
}
