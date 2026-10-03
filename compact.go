package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Compaction runs `/compact` in a separate process resumed on the session.
// Callers' processes run with --disable-slash-commands, under which Claude
// Code answers /compact with "isn't available in this environment", so the
// compaction process is the only one spawned without that flag.

const compactPrompt = "/compact"

// Compaction triggers.
const (
	TriggerAuto = "auto" // --auto-compact: CompactAfter after the last turn
	TriggerSize = "size" // --compact-above: the turn's context exceeded the threshold
)

// compactJob is a scheduled compaction: everything needed to resume the
// session, so it survives a daemon restart.
type compactJob struct {
	SessionID    string    `json:"session_id"`
	LastActivity time.Time `json:"last_activity"`
	Dir          string    `json:"dir"`
	Profile      Profile   `json:"profile"`
	Tag          string    `json:"tag,omitempty"`
	// ContextTokens is the context size the last turn ended on.
	ContextTokens int `json:"context_tokens"`
}

type runningCompaction struct {
	job     *compactJob
	trigger string
	started time.Time
	done    chan struct{}
	proc    *Proc // set once spawned; guarded by Pool.mu
}

func (rc *runningCompaction) kill() {
	if rc.proc != nil {
		rc.proc.Kill()
	}
}

// CompactionInfo describes a finished (or failed) compaction to the next
// turn on the session.
type CompactionInfo struct {
	Trigger string `json:"trigger"`
	// CompactedBeforeMS is how long before this turn started the compaction
	// finished.
	CompactedBeforeMS int64 `json:"compacted_before_ms"`
	// WaitedMS is how long this request waited for the compaction to finish.
	WaitedMS            int64   `json:"waited_ms,omitempty"`
	ContextTokensBefore int     `json:"context_tokens_before"`
	ContextTokensAfter  int     `json:"context_tokens_after"`
	DurationMS          int64   `json:"duration_ms"`
	CostUSD             float64 `json:"cost_usd"`
	// CacheReadTokens and InputTokens are what the compaction's API call
	// read from the prompt cache and paid for uncached: a cold compaction
	// shows up as a large InputTokens.
	CacheReadTokens int    `json:"cache_read_tokens"`
	InputTokens     int    `json:"input_tokens"`
	Error           string `json:"error,omitempty"`

	finished time.Time
}

// NextCompaction is what a turn arranged: a size compaction started now, or
// an auto compaction scheduled for At.
type NextCompaction struct {
	Trigger string    `json:"trigger"`
	At      time.Time `json:"at"`
}

type CompactionStatus struct {
	SessionID     string    `json:"session_id"`
	State         string    `json:"state"` // "pending" or "running"
	Trigger       string    `json:"trigger"`
	LastActivity  time.Time `json:"last_activity"`
	DueInS        int64     `json:"due_in_s,omitempty"`
	RunningS      int64     `json:"running_s,omitempty"`
	ContextTokens int       `json:"context_tokens"`
}

type compactAction int

const (
	compactWait compactAction = iota
	compactRun
	compactDrop
)

// compactDecision applies the cache-warmth rule to a scheduled compaction:
// wait until CompactAfter has passed since the last turn, run until
// CompactLatest, and drop it after that, since the cache may be cold and the
// compaction would pay for a full uncached read.
func compactDecision(last, now time.Time, after, latest time.Duration) compactAction {
	switch {
	case now.Before(last.Add(after)):
		return compactWait
	case now.Before(last.Add(latest)):
		return compactRun
	default:
		return compactDrop
	}
}

// afterTurnLocked updates the session's compaction plans for the turn that
// just finished and returns what it arranged. The session is still busy.
func (p *Pool) afterTurnLocked(sessionID string, proc *Proc, req AskRequest, contextTokens int) *NextCompaction {
	now := p.now()
	job := &compactJob{
		SessionID: sessionID, LastActivity: now, Dir: p.cfg.Dir,
		Profile: proc.Profile, Tag: req.Tag, ContextTokens: contextTokens,
	}
	_, hadPending := p.pending[sessionID]
	delete(p.pending, sessionID)
	var next *NextCompaction
	switch {
	case p.closed:
	case req.CompactAbove > 0 && contextTokens > req.CompactAbove:
		p.startCompactionLocked(job, TriggerSize)
		next = &NextCompaction{Trigger: TriggerSize, At: now}
	case req.AutoCompact:
		p.pending[sessionID] = job
		next = &NextCompaction{Trigger: TriggerAuto, At: now.Add(p.cfg.CompactAfter)}
	}
	if hadPending || (next != nil && next.Trigger == TriggerAuto) {
		p.savePendingLocked()
	}
	return next
}

// runDueCompactionsLocked starts the scheduled compactions that are due and
// drops the ones whose cache has gone cold. A session with a turn in flight
// is skipped: that turn reschedules or cancels its compaction.
func (p *Pool) runDueCompactionsLocked(now time.Time) {
	changed := false
	for id, info := range p.compacted {
		if now.Sub(info.finished) > 24*time.Hour {
			delete(p.compacted, id)
		}
	}
	for id, job := range p.pending {
		if p.busy[id] || p.running[id] != nil {
			continue
		}
		switch compactDecision(job.LastActivity, now, p.cfg.CompactAfter, p.cfg.CompactLatest) {
		case compactRun:
			p.startCompactionLocked(job, TriggerAuto)
		case compactDrop:
			log.Printf("compaction of %s dropped: last turn was %s ago, past %s, so the cache may be cold",
				id, now.Sub(job.LastActivity).Round(time.Second), p.cfg.CompactLatest)
			delete(p.pending, id)
			changed = true
		}
	}
	if changed {
		p.savePendingLocked()
	}
}

// startCompactionLocked marks the session as compacting and runs the
// compaction in the background. An auto job stays in pending, and on disk,
// until it finishes, so a restart mid-compaction retries it.
func (p *Pool) startCompactionLocked(job *compactJob, trigger string) {
	rc := &runningCompaction{job: job, trigger: trigger, started: p.now(), done: make(chan struct{})}
	p.running[job.SessionID] = rc
	// The live process has slash commands disabled; it is replaced once the
	// compaction is done.
	live := p.live[job.SessionID]
	delete(p.live, job.SessionID)
	if live != nil {
		live.proc.Kill()
	}
	log.Printf("compaction of %s started (%s, context %d tokens)", job.SessionID, trigger, job.ContextTokens)
	go p.runCompaction(rc, live)
}

func (p *Pool) runCompaction(rc *runningCompaction, live *liveEntry) {
	job := rc.job
	info := &CompactionInfo{Trigger: rc.trigger, ContextTokensBefore: job.ContextTokens}
	var resp *AskResponse
	err := func() error {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return errors.New("daemon is shutting down")
		}
		proc, err := spawn(spawnOpts{
			ClaudeBin: p.cfg.ClaudeBin, Dir: job.Dir, Profile: job.Profile,
			SessionID: job.SessionID, Resume: true, SlashCommands: true,
		})
		if err != nil {
			p.mu.Unlock()
			return err
		}
		rc.proc = proc
		p.mu.Unlock()
		defer proc.Kill()
		if err := proc.WaitReady(p.cfg.StartTimeout); err != nil {
			return fmt.Errorf("starting claude: %w", err)
		}
		tr, err := proc.Turn(compactPrompt, p.cfg.TurnTimeout)
		if err != nil {
			return err
		}
		var r struct {
			Result       string  `json:"result"`
			IsError      bool    `json:"is_error"`
			Subtype      string  `json:"subtype"`
			TotalCostUSD float64 `json:"total_cost_usd"`
			DurationMS   int64   `json:"duration_ms"`
			// A /compact result's usage is all zeros; the compaction
			// call's tokens are only in modelUsage. That covers this
			// process's calls, and so just the compaction, because the
			// daemon kills its processes: one that exits gracefully
			// writes a cost-state record, and a later resume starts
			// modelUsage and total_cost_usd from those session totals.
			ModelUsage json.RawMessage `json:"modelUsage"`
		}
		if err := json.Unmarshal(tr.Raw, &r); err != nil {
			return fmt.Errorf("parsing claude's result: %w", err)
		}
		info.CostUSD, info.DurationMS = r.TotalCostUSD, r.DurationMS
		var models map[string]struct {
			InputTokens          int `json:"inputTokens"`
			CacheReadInputTokens int `json:"cacheReadInputTokens"`
		}
		if json.Unmarshal(r.ModelUsage, &models) == nil {
			for _, m := range models {
				info.InputTokens += m.InputTokens
				info.CacheReadTokens += m.CacheReadInputTokens
			}
		}
		resp = &AskResponse{
			SessionID: job.SessionID, Source: SourceResume, Model: tr.Model, Result: r.Result,
			IsError: r.IsError, Subtype: r.Subtype, TotalCostUSD: r.TotalCostUSD,
			DurationMS: r.DurationMS, Usage: r.ModelUsage,
		}
		if tr.Compact == nil {
			// Claude Code reports a refused or failed /compact as an
			// ordinary result, without a compact boundary.
			return fmt.Errorf("claude did not compact: %q", r.Result)
		}
		info.ContextTokensAfter = tr.Compact.PostTokens
		if tr.Compact.PreTokens > 0 {
			info.ContextTokensBefore = tr.Compact.PreTokens
		}
		return nil
	}()

	p.mu.Lock()
	info.finished = p.now()
	if err != nil {
		info.Error = err.Error()
		log.Printf("compaction of %s (%s) FAILED: %v", job.SessionID, rc.trigger, err)
	} else {
		log.Printf("compaction of %s (%s) done: %d -> %d tokens, read %d cached + %d uncached, $%.4f",
			job.SessionID, rc.trigger, info.ContextTokensBefore, info.ContextTokensAfter,
			info.CacheReadTokens, info.InputTokens, info.CostUSD)
	}
	if !p.closed {
		p.compacted[job.SessionID] = info
		if p.pending[job.SessionID] == job {
			delete(p.pending, job.SessionID)
			p.savePendingLocked()
		}
		// Keep a live session live: its next turn should not pay for a resume.
		if live != nil {
			if proc, serr := spawn(spawnOpts{
				ClaudeBin: p.cfg.ClaudeBin, Dir: job.Dir, Profile: job.Profile,
				SessionID: job.SessionID, Resume: true,
			}); serr == nil {
				p.live[job.SessionID] = &liveEntry{proc: proc, lastUsed: live.lastUsed, ttl: live.ttl, priority: live.priority}
				p.evictLocked()
			} else {
				log.Printf("session %s: respawn after compaction failed: %v", job.SessionID, serr)
			}
		}
	}
	delete(p.running, job.SessionID)
	close(rc.done)
	p.mu.Unlock()

	if resp != nil && p.cfg.OnResult != nil {
		p.cfg.OnResult(resp, "compact:"+orNone(job.Tag))
	}
}

func (p *Pool) compactionStatusLocked(now time.Time) []CompactionStatus {
	out := []CompactionStatus{}
	for id, rc := range p.running {
		out = append(out, CompactionStatus{
			SessionID: id, State: "running", Trigger: rc.trigger, LastActivity: rc.job.LastActivity,
			RunningS: int64(now.Sub(rc.started).Seconds()), ContextTokens: rc.job.ContextTokens,
		})
	}
	for id, job := range p.pending {
		if p.running[id] != nil {
			continue
		}
		out = append(out, CompactionStatus{
			SessionID: id, State: "pending", Trigger: TriggerAuto, LastActivity: job.LastActivity,
			DueInS: int64(job.LastActivity.Add(p.cfg.CompactAfter).Sub(now).Seconds()), ContextTokens: job.ContextTokens,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// ---- persistence ----

func (p *Pool) savePendingLocked() {
	if p.cfg.StateFile == "" {
		return
	}
	jobs := make([]*compactJob, 0, len(p.pending))
	for _, j := range p.pending {
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].SessionID < jobs[j].SessionID })
	b, _ := json.MarshalIndent(jobs, "", "  ")
	tmp := p.cfg.StateFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("saving compactions: %v", err)
		return
	}
	if err := os.Rename(tmp, p.cfg.StateFile); err != nil {
		log.Printf("saving compactions: %v", err)
	}
}

// restorePendingLocked loads the compactions scheduled before a restart and
// sorts them by compactDecision: still waiting, due now, or too late.
func (p *Pool) restorePendingLocked() {
	if p.cfg.StateFile == "" {
		return
	}
	b, err := os.ReadFile(p.cfg.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Printf("loading compactions: %v", err)
		return
	}
	var jobs []*compactJob
	if err := json.Unmarshal(b, &jobs); err != nil {
		log.Printf("loading compactions from %s: %v", filepath.Base(p.cfg.StateFile), err)
		return
	}
	now := p.now()
	for _, j := range jobs {
		switch compactDecision(j.LastActivity, now, p.cfg.CompactAfter, p.cfg.CompactLatest) {
		case compactWait:
			log.Printf("compaction of %s restored, due in %s", j.SessionID, j.LastActivity.Add(p.cfg.CompactAfter).Sub(now).Round(time.Second))
			p.pending[j.SessionID] = j
		case compactRun:
			log.Printf("compaction of %s restored and due: running it now", j.SessionID)
			p.pending[j.SessionID] = j
		case compactDrop:
			log.Printf("compaction of %s dropped on restart: last turn was %s ago, past %s, so the cache may be cold",
				j.SessionID, now.Sub(j.LastActivity).Round(time.Second), p.cfg.CompactLatest)
		}
	}
	p.savePendingLocked()
	p.runDueCompactionsLocked(now)
}
