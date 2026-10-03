package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestCompactDecisionFollowsTheCacheWindows(t *testing.T) {
	after, latest := 55*time.Minute, 59*time.Minute
	for _, c := range []struct {
		since time.Duration
		want  compactAction
	}{
		{0, compactWait},
		{54*time.Minute + 59*time.Second, compactWait},
		{55 * time.Minute, compactRun},
		{58*time.Minute + 59*time.Second, compactRun},
		{59 * time.Minute, compactDrop},
		{3 * time.Hour, compactDrop},
	} {
		if got := compactDecision(t0, t0.Add(c.since), after, latest); got != c.want {
			t.Errorf("%s after the last turn: got %v, want %v", c.since, got, c.want)
		}
	}
}

func TestAutoCompactRunsWhenDueAndTheNextTurnHearsOfIt(t *testing.T) {
	h := newClockedHarness(t, t0, nil)
	a := h.askReq(AskRequest{Prompt: strings.Repeat("x", 1000), Profile: haiku, AutoCompact: true, KeepAlive: time.Hour})
	if a.ContextTokens != 1000 {
		t.Fatalf("context_tokens %d, want 1000", a.ContextTokens)
	}
	if n := a.NextCompaction; n == nil || n.Trigger != TriggerAuto || !n.At.Equal(t0.Add(55*time.Minute)) {
		t.Fatalf("next_compaction %+v, want auto at +55m", n)
	}

	h.clock.Advance(54 * time.Minute)
	h.pool.tick()
	if h.compactionsRun() != 0 {
		t.Fatal("compacted before it was due")
	}
	h.clock.Advance(2 * time.Minute)
	h.pool.tick()
	h.waitFor("compaction", func() bool { return h.compactionsRun() == 1 })
	h.waitIdle(a.SessionID)
	if h.pendingJob(a.SessionID) != nil {
		t.Fatal("compaction still pending after it ran")
	}
	if !h.isLive(a.SessionID) {
		t.Fatal("live session was not kept live after compaction")
	}

	b := h.askReq(AskRequest{Prompt: "yy", SessionID: a.SessionID, Profile: haiku})
	c := b.Compaction
	if c == nil || c.Trigger != TriggerAuto || c.ContextTokensBefore != 1000 || c.ContextTokensAfter != 100 || c.Error != "" || c.CacheReadTokens != 1000 {
		t.Fatalf("compaction %+v, want auto 1000 -> 100", c)
	}
	if b.Source != SourceLive || b.ContextTokens != 102 {
		t.Fatalf("got %s context %d, want the respawned live process on the compacted context", b.Source, b.ContextTokens)
	}
	if again := h.askReq(AskRequest{Prompt: "z", SessionID: a.SessionID, Profile: haiku}); again.Compaction != nil {
		t.Fatal("a compaction was reported twice")
	}
}

func TestALaterRequestReschedulesOrCancelsTheCompaction(t *testing.T) {
	h := newClockedHarness(t, t0, nil)
	a := h.askReq(AskRequest{Prompt: "a", Profile: haiku, AutoCompact: true})

	h.clock.Advance(30 * time.Minute)
	h.askReq(AskRequest{Prompt: "b", SessionID: a.SessionID, Profile: haiku, AutoCompact: true})
	if j := h.pendingJob(a.SessionID); j == nil || !j.LastActivity.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("pending %+v, want rescheduled from the second turn", j)
	}
	h.clock.Advance(30 * time.Minute) // 60m after the first turn, 30m after the second
	h.pool.tick()
	if h.compactionsRun() != 0 {
		t.Fatal("compacted on the first turn's schedule")
	}

	h.askReq(AskRequest{Prompt: "c", SessionID: a.SessionID, Profile: haiku})
	if h.pendingJob(a.SessionID) != nil {
		t.Fatal("a request without --auto-compact left the compaction scheduled")
	}
	var saved []compactJob
	b, _ := os.ReadFile(h.pool.cfg.StateFile)
	if err := json.Unmarshal(b, &saved); err != nil || len(saved) != 0 {
		t.Fatalf("state file %s, want an empty list", b)
	}
	h.clock.Advance(56 * time.Minute)
	h.pool.tick()
	if h.compactionsRun() != 0 {
		t.Fatal("a cancelled compaction ran")
	}
}

func TestRestartRestoresCompactionsByCacheWindow(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	waiting, due, cold := newSessionID(), newSessionID(), newSessionID()
	h := newClockedHarness(t, now, func(c *Config) {
		jobs := []compactJob{
			{SessionID: waiting, LastActivity: now.Add(-10 * time.Minute), Dir: c.Dir, Profile: haiku},
			{SessionID: due, LastActivity: now.Add(-57 * time.Minute), Dir: c.Dir, Profile: haiku},
			{SessionID: cold, LastActivity: now.Add(-70 * time.Minute), Dir: c.Dir, Profile: haiku},
		}
		b, _ := json.Marshal(jobs)
		if err := os.WriteFile(c.StateFile, b, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	h.waitFor("the due compaction", func() bool { return h.compactionsRun() == 1 })
	h.waitIdle(due)
	for _, l := range h.spawns() {
		if strings.HasSuffix(l, " slash") && !strings.HasPrefix(l, due+" ") {
			t.Fatalf("compacted the wrong session: %v", h.spawns())
		}
	}
	if h.pendingJob(waiting) == nil {
		t.Fatal("the waiting compaction was not restored")
	}
	if h.pendingJob(cold) != nil || h.pendingJob(due) != nil {
		t.Fatal("cold or finished compaction still pending")
	}
	var saved []compactJob
	b, _ := os.ReadFile(h.pool.cfg.StateFile)
	if err := json.Unmarshal(b, &saved); err != nil || len(saved) != 1 || saved[0].SessionID != waiting {
		t.Fatalf("state file %s, want only the waiting job", b)
	}
	// The restored job runs on its original schedule.
	h.clock.Advance(46 * time.Minute)
	h.pool.tick()
	h.waitFor("the restored compaction", func() bool { return h.compactionsRun() == 2 })
}

func TestARequestWaitsForARunningCompaction(t *testing.T) {
	t.Setenv("FAKE_COMPACT_DELAY", "300ms")
	h := newClockedHarness(t, t0, nil)
	a := h.askReq(AskRequest{Prompt: strings.Repeat("x", 500), Profile: haiku, CompactAbove: 400})
	if n := a.NextCompaction; n == nil || n.Trigger != TriggerSize {
		t.Fatalf("next_compaction %+v, want a size compaction", n)
	}
	b, err := h.pool.Ask(AskRequest{Prompt: "y", SessionID: a.SessionID, Profile: haiku, CompactAbove: 400})
	if err != nil {
		t.Fatalf("got %v, want to wait for the compaction", err)
	}
	c := b.Compaction
	if c == nil || c.Trigger != TriggerSize || c.WaitedMS < 200 || c.ContextTokensAfter != 50 {
		t.Fatalf("compaction %+v, want a size compaction this request waited for", c)
	}
	if b.ContextTokens != 51 || b.NextCompaction != nil {
		t.Fatalf("context %d next %+v, want the compacted context and no new compaction", b.ContextTokens, b.NextCompaction)
	}
	// Only the compaction process has slash commands enabled.
	for _, l := range h.spawns() {
		if strings.HasSuffix(l, " slash") != strings.Contains(l, a.SessionID+" haiku true slash") {
			t.Fatalf("unexpected spawn %q in %v", l, h.spawns())
		}
	}
	if h.compactionsRun() != 1 {
		t.Fatalf("%d compactions, want 1", h.compactionsRun())
	}
}

func TestConcurrentRequestsDuringACompactionAllWait(t *testing.T) {
	t.Setenv("FAKE_COMPACT_DELAY", "200ms")
	h := newClockedHarness(t, t0, nil)
	a := h.askReq(AskRequest{Prompt: strings.Repeat("x", 500), Profile: haiku, CompactAbove: 400})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.pool.Ask(AskRequest{Prompt: "y", SessionID: a.SessionID, Profile: haiku})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	var ok, busy int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrBusy):
			busy++
		default:
			t.Fatal(err)
		}
	}
	// After the compaction they race as any two turns do: one runs, and the
	// other runs after it or is refused as busy.
	if ok < 1 || ok+busy != 2 {
		t.Fatalf("ok %d busy %d", ok, busy)
	}
}

func TestAFailedCompactionIsReportedAndNotRetried(t *testing.T) {
	h := newClockedHarness(t, t0, nil)
	a := h.askReq(AskRequest{Prompt: "a", Profile: haiku, AutoCompact: true})
	// Break the binary, so the compaction's spawn fails.
	h.pool.mu.Lock()
	h.pool.cfg.ClaudeBin = "/nonexistent/claude"
	h.pool.mu.Unlock()
	h.clock.Advance(56 * time.Minute)
	h.pool.tick()
	h.waitIdle(a.SessionID)
	h.pool.mu.Lock()
	info := h.pool.compacted[a.SessionID]
	pending := h.pool.pending[a.SessionID]
	h.pool.mu.Unlock()
	if info == nil || info.Error == "" || pending != nil {
		t.Fatalf("info %+v pending %+v, want a reported failure and nothing pending", info, pending)
	}
}

func TestPrioritizedSessionsAreNotEvicted(t *testing.T) {
	h := newClockedHarness(t, t0, func(c *Config) { c.MaxLive = 2 })
	a := h.askReq(AskRequest{Prompt: "a", Profile: haiku, Priority: true})
	h.clock.Advance(time.Second)
	b := h.askReq(AskRequest{Prompt: "b", Profile: haiku})
	h.clock.Advance(time.Second)
	c := h.askReq(AskRequest{Prompt: "c", Profile: haiku})
	if !h.isLive(a.SessionID) || h.isLive(b.SessionID) || !h.isLive(c.SessionID) {
		t.Fatal("want the oldest unprioritized session evicted, not the prioritized one")
	}
}

func TestTheCapGivesWayWhenEverySessionIsPrioritized(t *testing.T) {
	h := newClockedHarness(t, t0, func(c *Config) { c.MaxLive = 1 })
	a := h.askReq(AskRequest{Prompt: "a", Profile: haiku, Priority: true})
	h.clock.Advance(time.Second)
	b := h.askReq(AskRequest{Prompt: "b", Profile: haiku, Priority: true})
	if !h.isLive(a.SessionID) || !h.isLive(b.SessionID) {
		t.Fatal("a prioritized session was evicted")
	}
	// An unprioritized newcomer is the one that goes.
	h.clock.Advance(time.Second)
	c := h.askReq(AskRequest{Prompt: "c", Profile: haiku})
	if h.isLive(c.SessionID) || !h.isLive(a.SessionID) || !h.isLive(b.SessionID) {
		t.Fatal("want only the unprioritized session evicted")
	}
}

func TestKeepAliveOverridesIdleTTLAndExpires(t *testing.T) {
	h := newClockedHarness(t, t0, func(c *Config) { c.IdleTTL = 15 * time.Minute })
	long := h.askReq(AskRequest{Prompt: "a", Profile: haiku, KeepAlive: 2 * time.Hour, Priority: true})
	short := h.askReq(AskRequest{Prompt: "b", Profile: haiku})
	h.clock.Advance(20 * time.Minute)
	h.pool.tick()
	if !h.isLive(long.SessionID) || h.isLive(short.SessionID) {
		t.Fatal("after 20m: want only the kept-alive session live")
	}
	h.clock.Advance(39 * time.Minute)
	h.pool.tick()
	if !h.isLive(long.SessionID) {
		t.Fatal("kept-alive session ended before its hour")
	}
	h.clock.Advance(2 * time.Minute) // keep-alive is capped at an hour
	h.pool.tick()
	if h.isLive(long.SessionID) {
		t.Fatal("prioritized session outlived its keep-alive")
	}
}

func TestARequestWithoutKeepAliveRevertsToIdleTTL(t *testing.T) {
	h := newClockedHarness(t, t0, func(c *Config) { c.IdleTTL = 15 * time.Minute })
	a := h.askReq(AskRequest{Prompt: "a", Profile: haiku, KeepAlive: time.Hour})
	h.askReq(AskRequest{Prompt: "b", SessionID: a.SessionID, Profile: haiku})
	h.clock.Advance(20 * time.Minute)
	h.pool.tick()
	if h.isLive(a.SessionID) {
		t.Fatal("keep-alive outlived the request that set it")
	}
}
