package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// A session's system prompt is fixed when it starts. Claude Code stores it
// with the session's transcript, and a process resumed on the session uses
// that copy: --system-prompt and --append-system-prompt are ignored (checked
// against Claude Code 2.1.289). A live process keeps the one it started with
// too. So a request that brings a different system prompt to an existing
// session would be answered under the old instructions while its caller
// believes it sent new ones; it is refused instead. New instructions need a
// new session. A request that leaves the system prompt out continues the
// session on its own.

var ErrSystemPromptChanged = errors.New("system prompt differs from the one the session started with")

// sessionPrompt is what is kept per session: a hash of its system prompt, and
// when it was last asked for, so sessions long gone can be forgotten.
type sessionPrompt struct {
	Hash     string    `json:"hash"`
	LastUsed time.Time `json:"last_used"`
}

// PromptMemory is how long a session's system prompt is remembered after its
// last request. A session resumed after that cannot be checked.
const PromptMemory = 30 * 24 * time.Hour

func promptHash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:8])
}

// checkPromptLocked refuses a request for an existing session whose system
// prompt is not the session's own. An empty one is no claim to new
// instructions, and a session this daemon never saw start cannot be checked.
func (p *Pool) checkPromptLocked(sessionID, prompt string) error {
	if prompt == "" {
		return nil
	}
	known, ok := p.prompts[sessionID]
	if !ok {
		log.Printf("session %s: cannot check its system prompt: it started before this daemon kept track, or was forgotten", sessionID)
		return nil
	}
	if known.Hash == promptHash(prompt) {
		return nil
	}
	log.Printf("session %s: REFUSED: the request's system prompt (%s) is not the one the session started with (%s), which Claude Code would keep; start a new session for new instructions",
		sessionID, promptHash(prompt), known.Hash)
	return fmt.Errorf("session %s: %w (%s, not %s): Claude Code keeps a session's system prompt, so start a new session for new instructions, or leave the system prompt out to continue under the old ones",
		sessionID, ErrSystemPromptChanged, promptHash(prompt), known.Hash)
}

// rememberPromptLocked records a new session's system prompt, or notes that
// an existing one was used.
func (p *Pool) rememberPromptLocked(sessionID string, started bool, prompt string) {
	now := p.now()
	if started {
		p.prompts[sessionID] = &sessionPrompt{Hash: promptHash(prompt), LastUsed: now}
	} else if known, ok := p.prompts[sessionID]; ok {
		known.LastUsed = now
	} else {
		return
	}
	p.savePromptsLocked()
}

func (p *Pool) savePromptsLocked() {
	if p.cfg.PromptsFile == "" {
		return
	}
	cutoff := p.now().Add(-PromptMemory)
	for id, known := range p.prompts {
		if known.LastUsed.Before(cutoff) {
			delete(p.prompts, id)
		}
	}
	b, _ := json.MarshalIndent(p.prompts, "", "  ")
	tmp := p.cfg.PromptsFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("saving session prompts: %v", err)
		return
	}
	if err := os.Rename(tmp, p.cfg.PromptsFile); err != nil {
		log.Printf("saving session prompts: %v", err)
	}
}

func (p *Pool) restorePromptsLocked() {
	if p.cfg.PromptsFile == "" {
		return
	}
	b, err := os.ReadFile(p.cfg.PromptsFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		err = json.Unmarshal(b, &p.prompts)
	}
	if err != nil {
		log.Printf("loading session prompts from %s: %v", filepath.Base(p.cfg.PromptsFile), err)
	}
}
