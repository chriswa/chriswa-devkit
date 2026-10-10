package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func withPrompt(prompt string) Profile {
	return Profile{Model: "haiku", Effort: "medium", SystemPrompt: prompt}
}

func TestADifferentSystemPromptForAnExistingSessionIsRefused(t *testing.T) {
	h := newHarness(t, nil)
	a := h.ask("", "a", withPrompt("You are Alice."))
	spawned := len(h.spawns())

	_, err := h.pool.Ask(AskRequest{Prompt: "b", SessionID: a.SessionID, Profile: withPrompt("You are Bob.")})
	if !errors.Is(err, ErrSystemPromptChanged) {
		t.Fatalf("got %v, want ErrSystemPromptChanged", err)
	}
	if len(h.spawns()) != spawned {
		t.Fatal("a refused request started a process")
	}
	// Refused before anything ran: the session is neither busy nor lost.
	if r := h.ask(a.SessionID, "c", withPrompt("You are Alice.")); r.Source != SourceLive || r.Result != "c|turn=2" {
		t.Fatalf("same prompt: got %s %q", r.Source, r.Result)
	}
	// Leaving it out continues the session under its own.
	if r := h.ask(a.SessionID, "d", withPrompt("")); r.Source != SourceLive {
		t.Fatalf("no prompt: got %s", r.Source)
	}
}

func TestASessionStartedWithNoSystemPromptCannotBeGivenOne(t *testing.T) {
	h := newHarness(t, nil)
	a := h.ask("", "a", haiku)
	if _, err := h.pool.Ask(AskRequest{Prompt: "b", SessionID: a.SessionID, Profile: withPrompt("You are Bob.")}); !errors.Is(err, ErrSystemPromptChanged) {
		t.Fatalf("got %v, want ErrSystemPromptChanged", err)
	}
}

func TestTheSystemPromptCheckSurvivesARestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "session-prompts.json")
	first := newHarness(t, func(c *Config) { c.PromptsFile = file })
	a := first.ask("", "a", withPrompt("You are Alice."))
	first.pool.Close()

	second := newHarness(t, func(c *Config) { c.PromptsFile = file })
	if _, err := second.pool.Ask(AskRequest{Prompt: "b", SessionID: a.SessionID, Profile: withPrompt("You are Bob.")}); !errors.Is(err, ErrSystemPromptChanged) {
		t.Fatalf("got %v, want ErrSystemPromptChanged", err)
	}
	if r := second.ask(a.SessionID, "c", withPrompt("You are Alice.")); r.Source != SourceResume {
		t.Fatalf("same prompt after a restart: got %s, want resume", r.Source)
	}
}

func TestASessionThisDaemonNeverSawStartCannotBeChecked(t *testing.T) {
	h := newHarness(t, nil)
	if r := h.ask(newSessionID(), "a", withPrompt("You are Bob.")); r.Source != SourceResume {
		t.Fatalf("got %s, want resume", r.Source)
	}
}

func TestASessionsPromptIsForgottenAMonthAfterItsLastRequest(t *testing.T) {
	file := filepath.Join(t.TempDir(), "session-prompts.json")
	h := newClockedHarness(t, time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), func(c *Config) { c.PromptsFile = file })
	old := h.ask("", "a", withPrompt("You are Alice."))
	h.clock.Advance(PromptMemory + time.Hour)
	h.ask("", "b", withPrompt("You are Carol.")) // saving prunes
	if r := h.ask(old.SessionID, "c", withPrompt("You are Bob.")); r.Result != "c|turn=2" {
		t.Fatalf("got %q", r.Result)
	}
}
