package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as a fake `claude -p` when CPD_FAKE_CLAUDE is set.
// It speaks the slice of stream-json the daemon uses and logs each spawn.
func TestMain(m *testing.M) {
	if os.Getenv("CPD_FAKE_CLAUDE") == "1" {
		fakeClaude()
		return
	}
	os.Exit(m.Run())
}

// The fake keeps each session's context size in a file under its working
// directory, so that it carries across processes as a transcript would:
// a turn adds its prompt's length, /compact cuts it to a tenth.
func fakeClaude() {
	var sessionID, model string
	resume, slash := false, true
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session-id":
			sessionID = args[i+1]
		case "--resume":
			sessionID, resume = args[i+1], true
		case "--model":
			model = args[i+1]
		case "--disable-slash-commands":
			slash = false
		}
	}
	if f, err := os.OpenFile(os.Getenv("FAKE_SPAWN_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		kind := "turn"
		if slash {
			kind = "slash"
		}
		fmt.Fprintf(f, "%s %s %v %s\n", sessionID, model, resume, kind)
		f.Close()
	}
	ctxFile := "ctx-" + sessionID
	readCtx := func() int {
		b, _ := os.ReadFile(ctxFile)
		n, _ := strconv.Atoi(string(b))
		return n
	}
	writeCtx := func(n int) { _ = os.WriteFile(ctxFile, []byte(strconv.Itoa(n)), 0o600) }
	if d := os.Getenv("FAKE_START_DELAY"); d != "" {
		dur, _ := time.ParseDuration(d)
		time.Sleep(dur)
	}
	enc := json.NewEncoder(os.Stdout)
	var encMu sync.Mutex
	out := struct{ Encode func(any) error }{func(v any) error {
		encMu.Lock()
		defer encMu.Unlock()
		return enc.Encode(v)
	}}
	sc := bufio.NewScanner(os.Stdin)
	turn := 0
	// A prompt starting "slow" takes FAKE_SLOW_TURN to answer, unless interrupted.
	var interrupt chan struct{}
	for sc.Scan() {
		var m struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
				Model   string `json:"model"`
			} `json:"request"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Type == "control_request" && m.Request.Subtype == "interrupt":
			out.Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m.RequestID}})
			if interrupt != nil {
				close(interrupt)
				interrupt = nil
			}
		case m.Type == "user" && strings.HasPrefix(m.Message.Content, "slow"):
			turn++
			writeCtx(readCtx() + len(m.Message.Content))
			out.Encode(map[string]any{"type": "system", "subtype": "init", "session_id": sessionID, "model": model})
			stop := make(chan struct{})
			interrupt = stop
			content, n := m.Message.Content, turn
			go func() {
				dur, _ := time.ParseDuration(os.Getenv("FAKE_SLOW_TURN"))
				select {
				case <-stop:
					out.Encode(map[string]any{"type": "user", "message": map[string]any{"role": "user",
						"content": []map[string]any{{"type": "text", "text": "[Request interrupted by user]"}}}})
					out.Encode(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "session_id": sessionID})
				case <-time.After(dur):
					out.Encode(map[string]any{"type": "result", "subtype": "success", "session_id": sessionID,
						"result": fmt.Sprintf("%s|turn=%d", content, n)})
				}
			}()
		case m.Type == "control_request" && m.Request.Subtype == "set_model":
			model = m.Request.Model
			out.Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m.RequestID}})
		case m.Type == "control_request":
			out.Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m.RequestID, "response": map[string]any{}}})
		case m.Type == "user" && m.Message.Content == "/compact":
			out.Encode(map[string]any{"type": "system", "subtype": "init", "session_id": sessionID, "model": model})
			if !slash {
				out.Encode(map[string]any{"type": "result", "subtype": "success", "session_id": sessionID,
					"result": "/compact isn't available in this environment."})
				continue
			}
			if d := os.Getenv("FAKE_COMPACT_DELAY"); d != "" {
				dur, _ := time.ParseDuration(d)
				time.Sleep(dur)
			}
			pre := readCtx()
			post := max(pre/10, 1)
			writeCtx(post)
			out.Encode(map[string]any{"type": "system", "subtype": "compact_boundary", "session_id": sessionID,
				"compact_metadata": map[string]any{"trigger": "manual", "pre_tokens": pre, "post_tokens": post}})
			out.Encode(map[string]any{"type": "result", "subtype": "success", "session_id": sessionID,
				"result": "", "local_command": "compact", "total_cost_usd": 0.002,
				"modelUsage": map[string]any{model: map[string]any{"inputTokens": 3, "cacheReadInputTokens": pre}}})
		case m.Type == "user":
			turn++
			ctx := readCtx() + len(m.Message.Content)
			writeCtx(ctx)
			out.Encode(map[string]any{"type": "system", "subtype": "init", "session_id": sessionID, "model": model})
			out.Encode(map[string]any{"type": "assistant", "message": map[string]any{"model": model,
				"usage": map[string]any{"input_tokens": 1, "cache_read_input_tokens": ctx - 1, "cache_creation_input_tokens": 0}}})
			out.Encode(map[string]any{
				"type": "result", "subtype": "success", "session_id": sessionID,
				"result":         fmt.Sprintf("%s|turn=%d", m.Message.Content, turn),
				"total_cost_usd": 0.001, "usage": map[string]any{"input_tokens": 1},
			})
		}
	}
}

type harness struct {
	t        *testing.T
	pool     *Pool
	spawnLog string
	clock    *fakeClock
	dir      string
}

// fakeClock drives the pool's TTL and compaction clock; spares still age in
// real time.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newHarness(t *testing.T, mutate func(*Config)) *harness {
	t.Helper()
	dir := t.TempDir()
	spawnLog := filepath.Join(dir, "spawns.log")
	t.Setenv("CPD_FAKE_CLAUDE", "1")
	t.Setenv("FAKE_SPAWN_LOG", spawnLog)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ClaudeBin:      self,
		Dir:            dir,
		SpareTTL:       time.Hour,
		IdleTTL:        time.Hour,
		MaxLive:        5,
		ProfileIdleTTL: time.Hour,
		StartTimeout:   5 * time.Second,
		TurnTimeout:    5 * time.Second,
		TickInterval:   time.Hour, // tests call tick() themselves
		RetryBackoff:   time.Hour,
		Defaults:       []Profile{haiku},
		CompactAfter:   55 * time.Minute,
		CompactLatest:  59 * time.Minute,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	h := &harness{t: t, pool: NewPool(cfg), spawnLog: spawnLog, dir: dir}
	t.Cleanup(h.pool.Close)
	return h
}

var haiku = Profile{Model: "haiku", Effort: "medium"}

func (h *harness) ask(sessionID, prompt string, prof Profile) *AskResponse {
	h.t.Helper()
	return h.askReq(AskRequest{Prompt: prompt, SessionID: sessionID, Profile: prof})
}

func (h *harness) askReq(req AskRequest) *AskResponse {
	h.t.Helper()
	r, err := h.pool.Ask(req)
	if err != nil {
		h.t.Fatalf("ask: %v", err)
	}
	return r
}

// newClockedHarness is a harness whose pool reads a fake clock starting at
// start, and which persists compactions to a state file.
func newClockedHarness(t *testing.T, start time.Time, mutate func(*Config)) *harness {
	t.Helper()
	clock := &fakeClock{t: start}
	stateDir := t.TempDir()
	h := newHarness(t, func(c *Config) {
		c.Now = clock.Now
		c.StateFile = filepath.Join(stateDir, "compactions.json")
		if mutate != nil {
			mutate(c)
		}
	})
	h.clock = clock
	return h
}

func (h *harness) isLive(id string) bool {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	return h.pool.live[id] != nil
}

func (h *harness) pendingJob(id string) *compactJob {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	return h.pool.pending[id]
}

func (h *harness) compactionsRun() int {
	n := 0
	for _, l := range h.spawns() {
		if strings.HasSuffix(l, " slash") {
			n++
		}
	}
	return n
}

func (h *harness) waitIdle(id string) {
	h.t.Helper()
	h.waitFor("compaction to finish", func() bool {
		h.pool.mu.Lock()
		defer h.pool.mu.Unlock()
		return h.pool.running[id] == nil
	})
}

func (h *harness) spawns() []string {
	b, _ := os.ReadFile(h.spawnLog)
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// waitFor polls cond, since spares start in the background.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	h.t.Fatalf("timed out waiting for %s", what)
}

func (h *harness) spareReady(prof Profile) bool {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	s := h.pool.slots[prof.key()]
	return s != nil && s.current != nil && s.current.IsReady() && s.replacing == nil
}

func (h *harness) spareSession(prof Profile) string {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	if s := h.pool.slots[prof.key()]; s != nil && s.current != nil {
		return s.current.SessionID
	}
	return ""
}

func TestNewRequestClaimsTheSpareAndRefills(t *testing.T) {
	h := newHarness(t, nil)
	h.waitFor("spare", func() bool { return h.spareReady(haiku) })
	spare := h.spareSession(haiku)

	r := h.ask("", "hi", haiku)
	if r.Source != SourceSpare || r.SessionID != spare {
		t.Fatalf("got source %s session %s, want the spare %s", r.Source, r.SessionID, spare)
	}
	h.waitFor("refill", func() bool { return h.spareReady(haiku) })
	if h.spareSession(haiku) == spare {
		t.Fatal("refilled spare reuses the claimed session")
	}
}

func TestContinuingALiveSessionReusesItsProcess(t *testing.T) {
	h := newHarness(t, nil)
	first := h.ask("", "one", haiku)
	second := h.ask(first.SessionID, "two", haiku)
	if second.Source != SourceLive || second.Result != "two|turn=2" {
		t.Fatalf("got %s %q, want the live process's second turn", second.Source, second.Result)
	}
}

func TestConcurrentNewRequestsSplitBetweenSpareAndCold(t *testing.T) {
	h := newHarness(t, nil)
	h.waitFor("spare", func() bool { return h.spareReady(haiku) })
	var wg sync.WaitGroup
	sources := make(chan Source, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sources <- h.ask("", "x", haiku).Source
		}()
	}
	wg.Wait()
	close(sources)
	got := map[Source]int{}
	for s := range sources {
		got[s]++
	}
	// The second request may find the refill still starting and claim it,
	// which also counts as a spare; it must never wait on a cold spawn twice.
	if got[SourceSpare] < 1 || got[SourceSpare]+got[SourceCold] != 2 {
		t.Fatalf("sources %v", got)
	}
}

func TestLiveSessionsBeyondTheCapAreEvictedOldestFirst(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxLive = 2 })
	a := h.ask("", "a", haiku)
	b := h.ask("", "b", haiku)
	h.ask("", "c", haiku)
	if r := h.ask(b.SessionID, "b2", haiku); r.Source != SourceLive {
		t.Fatalf("b: got %s, want live", r.Source)
	}
	if r := h.ask(a.SessionID, "a2", haiku); r.Source != SourceResume {
		t.Fatalf("a: got %s, want resume after eviction", r.Source)
	}
}

func TestIdleSessionsAreShutDown(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.IdleTTL = 10 * time.Millisecond })
	a := h.ask("", "a", haiku)
	time.Sleep(20 * time.Millisecond)
	h.pool.tick()
	if r := h.ask(a.SessionID, "a2", haiku); r.Source != SourceResume {
		t.Fatalf("got %s, want resume", r.Source)
	}
}

func TestAgedSpareIsReplacedOnlyOnceTheNewOneIsReady(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.SpareTTL = 10 * time.Millisecond })
	h.waitFor("spare", func() bool { return h.spareReady(haiku) })
	old := h.spareSession(haiku)
	time.Sleep(20 * time.Millisecond)

	t.Setenv("FAKE_START_DELAY", "200ms")
	h.pool.tick()
	if got := h.spareSession(haiku); got != old {
		t.Fatalf("spare changed to %s before its replacement was ready", got)
	}
	h.waitFor("replacement", func() bool { return h.spareReady(haiku) && h.spareSession(haiku) != old })
	if n := len(h.spawns()); n != 2 {
		t.Fatalf("%d spawns, want the original and one replacement", n)
	}
}

func TestClaimDuringReplacementPromotesTheReplacement(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.SpareTTL = 10 * time.Millisecond })
	h.waitFor("spare", func() bool { return h.spareReady(haiku) })
	old := h.spareSession(haiku)
	time.Sleep(20 * time.Millisecond)

	t.Setenv("FAKE_START_DELAY", "200ms")
	h.pool.tick() // replacement starts
	r := h.ask("", "x", haiku)
	if r.SessionID != old {
		t.Fatalf("claimed %s, want the old spare %s", r.SessionID, old)
	}
	h.waitFor("promoted replacement", func() bool { return h.spareReady(haiku) })
	if n := len(h.spawns()); n != 2 {
		t.Fatalf("%d spawns, want the replacement to serve as the refill", n)
	}
}

func TestConcurrentTurnsOnOneSessionAreRefused(t *testing.T) {
	h := newHarness(t, nil)
	a := h.ask("", "a", haiku)
	h.pool.mu.Lock()
	h.pool.busy[a.SessionID] = true
	h.pool.mu.Unlock()
	_, err := h.pool.Ask(AskRequest{Prompt: "x", SessionID: a.SessionID, Profile: haiku})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
}

func TestModelChangeOnALiveSessionSwitchesInPlace(t *testing.T) {
	h := newHarness(t, nil)
	a := h.ask("", "a", haiku)
	r := h.ask(a.SessionID, "b", Profile{Model: "sonnet", Effort: "medium"})
	if r.Source != SourceLive || r.Model != "sonnet" || r.Result != "b|turn=2" {
		t.Fatalf("got %s %s %q", r.Source, r.Model, r.Result)
	}
}

func TestEffortChangeOnALiveSessionResumesInANewProcess(t *testing.T) {
	h := newHarness(t, nil)
	a := h.ask("", "a", haiku)
	r := h.ask(a.SessionID, "b", Profile{Model: "haiku", Effort: "high"})
	if r.Source != SourceResume {
		t.Fatalf("got %s, want resume", r.Source)
	}
}

func TestUnrequestedNonDefaultProfilesAreDropped(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.ProfileIdleTTL = 10 * time.Millisecond })
	custom := Profile{Model: "haiku", Effort: "medium", SystemPrompt: "be brief"}
	h.ask("", "x", custom)
	h.waitFor("custom spare", func() bool { return h.spareReady(custom) })
	time.Sleep(20 * time.Millisecond)
	h.pool.tick()
	if h.spareSession(custom) != "" {
		t.Fatal("custom profile's spare survived its idle TTL")
	}
	if !h.spareReady(haiku) {
		t.Fatal("default profile's spare was dropped")
	}
}

func TestSpawnedClaudeDoesNotInheritAnAgentSessionsIdentity(t *testing.T) {
	got := scrubbedEnv([]string{"CLAUDECODE=1", "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CONFIG_DIR=/x", "PATH=/bin"})
	if strings.Join(got, " ") != "CLAUDE_CONFIG_DIR=/x PATH=/bin" {
		t.Fatalf("got %v", got)
	}
}

// askAsync starts a named turn and returns its outcome on a channel.
func (h *harness) askAsync(req AskRequest) <-chan *AskResponse {
	ch := make(chan *AskResponse, 1)
	go func() {
		r, err := h.pool.Ask(req)
		if err != nil {
			h.t.Errorf("ask: %v", err)
		}
		ch <- r
	}()
	return ch
}

func (h *harness) turnRunning(turnID string) bool {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	t := h.pool.turns[turnID]
	return t != nil && t.early.IsZero()
}

func TestAbortInterruptsATurnAndKeepsItsPromptInTheSession(t *testing.T) {
	t.Setenv("FAKE_SLOW_TURN", "5s")
	h := newHarness(t, nil)
	a := h.ask("", "first", haiku)
	done := h.askAsync(AskRequest{Prompt: "slow question", SessionID: a.SessionID, Profile: haiku, TurnID: "t1"})
	h.waitFor("turn to start", func() bool { return h.turnRunning("t1") })
	time.Sleep(50 * time.Millisecond)
	if !h.pool.Abort("t1") {
		t.Fatal("Abort reported no turn running")
	}
	r := <-done
	if r.Abort == nil || !r.Abort.PromptInSession || r.Subtype != "aborted" || r.IsError || r.Result != "" {
		t.Fatalf("got %+v (abort %+v), want an abort with the prompt in the session", r, r.Abort)
	}
	// The session goes on in the same process, after the interrupted turn.
	next := h.ask(a.SessionID, "after", haiku)
	if next.Source != SourceLive || next.Result != "after|turn=3" {
		t.Fatalf("next turn: source %s result %q, want the live process's third turn", next.Source, next.Result)
	}
}

func TestAbortArrivingBeforeItsTurnLeavesTheSessionUntouched(t *testing.T) {
	h := newHarness(t, nil)
	a := h.ask("", "first", haiku)
	if h.pool.Abort("t2") {
		t.Fatal("Abort reported a turn running before any was asked")
	}
	r := h.askReq(AskRequest{Prompt: "never sent", SessionID: a.SessionID, Profile: haiku, TurnID: "t2"})
	if r.Abort == nil || r.Abort.PromptInSession || r.Subtype != "aborted" {
		t.Fatalf("got %+v (abort %+v), want an abort before the prompt was sent", r, r.Abort)
	}
	next := h.ask(a.SessionID, "after", haiku)
	if next.Result != "after|turn=2" {
		t.Fatalf("next turn: %q, want the session's second turn", next.Result)
	}
}

func TestATurnFinishedBeforeItsAbortIsReportedWhole(t *testing.T) {
	h := newHarness(t, nil)
	a := h.ask("", "first", haiku)
	r := h.askReq(AskRequest{Prompt: "quick", SessionID: a.SessionID, Profile: haiku, TurnID: "t3"})
	h.pool.Abort("t3")
	if r.Abort != nil || r.Result != "quick|turn=2" {
		t.Fatalf("got %+v, want the whole reply", r)
	}
}
