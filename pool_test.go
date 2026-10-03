package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func fakeClaude() {
	var sessionID, model, loaded, forkFrom string
	resume, fork := false, false
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session-id":
			sessionID = args[i+1]
		case "--resume":
			loaded, resume = args[i+1], true
		case "--fork-session":
			fork = true
		case "--model":
			model = args[i+1]
		}
	}
	// --resume names the session to load; with --fork-session the process
	// continues as the one --session-id names, otherwise as the loaded one.
	if fork {
		forkFrom = loaded
	} else if resume {
		sessionID = loaded
	}
	cwd, _ := os.Getwd()
	if f, err := os.OpenFile(os.Getenv("FAKE_SPAWN_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintf(f, "%s %s %v fork=%s dir=%s\n", sessionID, model, resume, forkFrom, cwd)
		f.Close()
	}
	if d := os.Getenv("FAKE_START_DELAY"); d != "" {
		dur, _ := time.ParseDuration(d)
		time.Sleep(dur)
	}
	out := json.NewEncoder(os.Stdout)
	sc := bufio.NewScanner(os.Stdin)
	turn := 0
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
		case m.Type == "control_request" && m.Request.Subtype == "set_model":
			model = m.Request.Model
			out.Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m.RequestID}})
		case m.Type == "control_request":
			out.Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m.RequestID, "response": map[string]any{}}})
		case m.Type == "user":
			turn++
			out.Encode(map[string]any{"type": "system", "subtype": "init", "session_id": sessionID, "model": model})
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
	cfg      Config
	pool     *Pool
	spawnLog string
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
	}
	if mutate != nil {
		mutate(&cfg)
	}
	h := &harness{t: t, cfg: cfg, pool: NewPool(cfg), spawnLog: spawnLog}
	t.Cleanup(h.pool.Close)
	return h
}

var haiku = Profile{Model: "haiku", Effort: "medium"}

func (h *harness) ask(sessionID, prompt string, prof Profile) *AskResponse {
	h.t.Helper()
	r, err := h.pool.Ask(AskRequest{Prompt: prompt, SessionID: sessionID, Profile: prof})
	if err != nil {
		h.t.Fatalf("ask: %v", err)
	}
	return r
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

// spawnFor is the spawn log line of the last process that ran sessionID.
func (h *harness) spawnFor(sessionID string) (line, dir string) {
	h.t.Helper()
	for _, l := range h.spawns() {
		if strings.HasPrefix(l, sessionID+" ") {
			line = l
		}
	}
	if line == "" {
		h.t.Fatalf("no spawn for %s in %v", sessionID, h.spawns())
	}
	_, dir, _ = strings.Cut(line, " dir=")
	return line, dir
}

// sameDir compares directories through symlinks, since macOS's temp
// directories are reached through /var -> /private/var.
func sameDir(t *testing.T, got, want string) bool {
	t.Helper()
	g, err1 := filepath.EvalSymlinks(got)
	w, err2 := filepath.EvalSymlinks(want)
	return err1 == nil && err2 == nil && g == w
}

func (h *harness) fork(source, dir, prompt string) *AskResponse {
	h.t.Helper()
	r, err := h.pool.Ask(AskRequest{Prompt: prompt, ForkFrom: source, Dir: dir, Profile: haiku})
	if err != nil {
		h.t.Fatalf("fork: %v", err)
	}
	return r
}

const kevin = "11111111-1111-4111-8111-111111111111"

func TestForkRunsInTheSourcesDirectoryWithoutTouchingTheSpare(t *testing.T) {
	h := newHarness(t, nil)
	h.waitFor("spare", func() bool { return h.spareReady(haiku) })
	spare := h.spareSession(haiku)
	project := t.TempDir()

	r := h.fork(kevin, project, "what are you doing?")
	if r.Source != SourceFork || r.ForkedFrom != kevin {
		t.Fatalf("got source %s forked_from %q, want a fork of %s", r.Source, r.ForkedFrom, kevin)
	}
	if r.SessionID == kevin || r.SessionID == spare || r.SessionID == "" {
		t.Fatalf("fork reported session %q; want a new id (source %s, spare %s)", r.SessionID, kevin, spare)
	}
	line, dir := h.spawnFor(r.SessionID)
	if !strings.Contains(line, "fork="+kevin) || !sameDir(t, dir, project) {
		t.Fatalf("fork spawned as %q, want a fork of %s in %s", line, kevin, project)
	}
	if got := h.spareSession(haiku); got != spare || !h.spareReady(haiku) {
		t.Fatalf("spare changed from %s to %s", spare, got)
	}
	if n := len(h.spawns()); n != 2 {
		t.Fatalf("%d spawns, want the spare and the fork", n)
	}
}

func TestFollowUpOnAForkReusesItsLiveProcess(t *testing.T) {
	h := newHarness(t, nil)
	f := h.fork(kevin, t.TempDir(), "one")
	r := h.ask(f.SessionID, "two", haiku)
	if r.Source != SourceLive || r.SessionID != f.SessionID || r.Result != "two|turn=2" || r.ForkedFrom != "" {
		t.Fatalf("got %s %s %q forked_from=%q, want the fork's live second turn", r.Source, r.SessionID, r.Result, r.ForkedFrom)
	}
}

func TestForkIsResumedInItsDirectoryAfterARestart(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.ForkLog = filepath.Join(c.Dir, "forks.jsonl") })
	project := t.TempDir()
	f := h.fork(kevin, project, "one")
	h.pool.Close()

	restarted := NewPool(h.cfg)
	t.Cleanup(restarted.Close)
	r, err := restarted.Ask(AskRequest{Prompt: "two", SessionID: f.SessionID, Profile: haiku})
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != SourceResume || r.SessionID != f.SessionID {
		t.Fatalf("got %s %s, want a resume of %s", r.Source, r.SessionID, f.SessionID)
	}
	line, dir := h.spawnFor(f.SessionID)
	if !strings.Contains(line, " true fork= ") || !sameDir(t, dir, project) {
		t.Fatalf("resumed as %q, want a plain resume in %s", line, project)
	}
}

func TestEffortChangeOnAForkResumesInItsDirectory(t *testing.T) {
	h := newHarness(t, nil)
	project := t.TempDir()
	f := h.fork(kevin, project, "one")
	r := h.ask(f.SessionID, "two", Profile{Model: "haiku", Effort: "high"})
	if r.Source != SourceResume {
		t.Fatalf("got %s, want resume", r.Source)
	}
	if _, dir := h.spawnFor(f.SessionID); !sameDir(t, dir, project) {
		t.Fatalf("resumed in %s, want %s", dir, project)
	}
}

func TestUnknownSessionIsResumedInTheDaemonsDirectory(t *testing.T) {
	h := newHarness(t, nil)
	r := h.ask(kevin, "hi", haiku)
	if r.Source != SourceResume {
		t.Fatalf("got %s, want resume", r.Source)
	}
	if _, dir := h.spawnFor(kevin); !sameDir(t, dir, h.cfg.Dir) {
		t.Fatalf("resumed in %s, want the daemon's %s", dir, h.cfg.Dir)
	}
}

func TestBadForkRequestsAreRefusedBeforeSpawning(t *testing.T) {
	h := newHarness(t, nil)
	h.waitFor("spare", func() bool { return h.spareReady(haiku) })
	for name, req := range map[string]AskRequest{
		"relative dir":  {Prompt: "x", ForkFrom: kevin, Dir: "project", Profile: haiku},
		"missing dir":   {Prompt: "x", ForkFrom: kevin, Dir: filepath.Join(t.TempDir(), "gone"), Profile: haiku},
		"with -s":       {Prompt: "x", ForkFrom: kevin, SessionID: kevin, Dir: t.TempDir(), Profile: haiku},
		"no dir at all": {Prompt: "x", ForkFrom: kevin, Profile: haiku},
	} {
		if _, err := h.pool.Ask(req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if n := len(h.spawns()); n != 1 {
		t.Fatalf("%d spawns, want only the spare", n)
	}
	if st := h.pool.Status(); len(st.Busy) != 0 {
		t.Fatalf("busy after refusals: %v", st.Busy)
	}
}
