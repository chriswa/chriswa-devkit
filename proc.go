package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Profile is everything about a claude process that is fixed when it is
// spawned. Two requests can share a spare only if their profiles are equal.
type Profile struct {
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	SystemPrompt string `json:"system_prompt"`
	// NoThinking sets MAX_THINKING_TOKENS=0. Claude Code otherwise lets
	// models think, which costs latency a quick reply cannot afford.
	NoThinking bool `json:"no_thinking"`
}

func (p Profile) key() string {
	b, _ := json.Marshal(p)
	return string(b)
}

// Proc is one `claude -p` process speaking stream-json on stdin/stdout.
type Proc struct {
	SessionID string
	Profile   Profile
	Created   time.Time

	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan []byte

	ready     chan struct{} // closed when the initialize handshake succeeds
	readyErr  error
	dead      chan struct{} // closed when the process exits
	deadErr   error
	killOnce  sync.Once
	readyOnce sync.Once

	// model is the resolved model id, read from the system/init message.
	model string
}

type spawnOpts struct {
	ClaudeBin string
	Dir       string
	Profile   Profile
	SessionID string
	Resume    bool
	// ForkFrom, when set, starts SessionID as a fork of this session: Claude
	// Code copies its history into a new transcript and never writes to the
	// source's. The source must have been filed under Dir.
	ForkFrom string
}

const initRequestID = "cpd-init"

// inheritedSessionVars are the variables Claude Code stamps on its own child
// processes to identify the session they belong to. They reach the daemon
// whenever it is started from inside an agent, and a claude that inherits
// them takes itself for a nested child of a session that never spawned it.
// Mirrors spaceterm's src/server/spawn-env.ts, plus the variables Claude Code
// sets for tool subprocesses.
var inheritedSessionVars = map[string]bool{
	"CLAUDECODE":                    true,
	"CLAUDE_PID":                    true,
	"CLAUDE_CODE_CHILD_SESSION":     true,
	"CLAUDE_CODE_SESSION_ID":        true,
	"CLAUDE_CODE_BRIDGE_SESSION_ID": true,
	"CLAUDE_CODE_ENTRYPOINT":        true,
	"CLAUDE_CODE_EXECPATH":          true,
	"CLAUDE_CODE_MESSAGING_SOCKET":  true,
	"CLAUDE_CODE_MESSAGING_TOKEN":   true,
	"CLAUDE_CODE_SSE_PORT":          true,
	"CLAUDE_CODE_SESSION_ATTENDED":  true,
	"CLAUDE_SESSION_ID":             true,
	"CLAUDE_CWD":                    true,
	"CLAUDE_EFFORT":                 true,
}

func scrubbedEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !inheritedSessionVars[name] {
			out = append(out, kv)
		}
	}
	return out
}

func spawn(o spawnOpts) (*Proc, error) {
	args := []string{"-p"}
	if o.ForkFrom != "" {
		// --session-id names the fork up front, so the pool can track it
		// before the first turn reports it.
		args = append(args, "--resume", o.ForkFrom, "--fork-session", "--session-id", o.SessionID)
	} else if o.Resume {
		args = append(args, "--resume", o.SessionID)
	} else {
		args = append(args, "--session-id", o.SessionID)
	}
	if o.Profile.Model != "" {
		args = append(args, "--model", o.Profile.Model)
	}
	if o.Profile.Effort != "" {
		args = append(args, "--effort", o.Profile.Effort)
	}
	args = append(args,
		"--system-prompt", o.Profile.SystemPrompt,
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources", "",
		"--disable-slash-commands",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	)
	cmd := exec.Command(o.ClaudeBin, args...)
	cmd.Dir = o.Dir
	cmd.Env = append(scrubbedEnv(os.Environ()), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	if o.Profile.NoThinking {
		cmd.Env = append(cmd.Env, "MAX_THINKING_TOKENS=0")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tailBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Proc{
		SessionID: o.SessionID,
		Profile:   o.Profile,
		Created:   time.Now(),
		cmd:       cmd,
		stdin:     stdin,
		lines:     make(chan []byte, 64),
		ready:     make(chan struct{}),
		dead:      make(chan struct{}),
	}
	go p.readLoop(stdout)
	go func() {
		err := cmd.Wait()
		if err == nil {
			err = errors.New("claude exited")
		}
		if s := stderr.String(); s != "" {
			err = fmt.Errorf("%w: %s", err, s)
		}
		p.deadErr = err
		close(p.dead)
		p.markReady(err)
	}()
	// The handshake doubles as a readiness probe: a fresh process prints
	// nothing until it is asked something.
	if err := p.write(map[string]any{
		"type":       "control_request",
		"request_id": initRequestID,
		"request":    map[string]any{"subtype": "initialize"},
	}); err != nil {
		p.Kill()
		return nil, err
	}
	return p, nil
}

func (p *Proc) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		var head struct {
			Type     string `json:"type"`
			Response struct {
				RequestID string `json:"request_id"`
				Subtype   string `json:"subtype"`
				Error     string `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &head) == nil && head.Type == "control_response" && head.Response.RequestID == initRequestID {
			if head.Response.Subtype == "success" {
				p.markReady(nil)
			} else {
				p.markReady(fmt.Errorf("initialize failed: %s", head.Response.Error))
			}
			continue
		}
		p.lines <- line
	}
	close(p.lines)
}

func (p *Proc) markReady(err error) {
	p.readyOnce.Do(func() {
		p.readyErr = err
		close(p.ready)
	})
}

func (p *Proc) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = p.stdin.Write(append(b, '\n'))
	return err
}

// WaitReady blocks until the handshake completes or the process dies.
func (p *Proc) WaitReady(timeout time.Duration) error {
	select {
	case <-p.ready:
		return p.readyErr
	case <-time.After(timeout):
		return errors.New("timed out waiting for claude to start")
	}
}

func (p *Proc) IsReady() bool {
	select {
	case <-p.ready:
		return p.readyErr == nil
	default:
		return false
	}
}

func (p *Proc) Alive() bool {
	select {
	case <-p.dead:
		return false
	default:
		return true
	}
}

func (p *Proc) Kill() {
	p.killOnce.Do(func() {
		_ = p.stdin.Close()
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	})
}

// SetModel switches a live process's model. Claude Code records the switch
// in the transcript, so this is only used to continue an existing session,
// never to adapt a spare.
func (p *Proc) SetModel(model string, timeout time.Duration) error {
	id := fmt.Sprintf("cpd-model-%d", time.Now().UnixNano())
	if err := p.write(map[string]any{
		"type":       "control_request",
		"request_id": id,
		"request":    map[string]any{"subtype": "set_model", "model": model},
	}); err != nil {
		return err
	}
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				return p.deathError()
			}
			var m struct {
				Type     string `json:"type"`
				Response struct {
					RequestID string `json:"request_id"`
					Subtype   string `json:"subtype"`
					Error     string `json:"error"`
				} `json:"response"`
			}
			if json.Unmarshal(line, &m) == nil && m.Type == "control_response" && m.Response.RequestID == id {
				if m.Response.Subtype != "success" {
					return fmt.Errorf("set_model failed: %s", m.Response.Error)
				}
				p.Profile.Model = model
				return nil
			}
		case <-deadline:
			return errors.New("timed out switching model")
		}
	}
}

// TurnResult is the `result` message Claude Code emits at the end of a turn,
// plus the model it reported in system/init.
type TurnResult struct {
	Raw   json.RawMessage
	Model string
}

// Turn sends one user message and waits for the turn's result.
func (p *Proc) Turn(prompt string, timeout time.Duration) (*TurnResult, error) {
	if err := p.write(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": prompt},
	}); err != nil {
		return nil, fmt.Errorf("writing prompt: %w", err)
	}
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				return nil, p.deathError()
			}
			var m struct {
				Type    string `json:"type"`
				Subtype string `json:"subtype"`
				Model   string `json:"model"`
			}
			if json.Unmarshal(line, &m) != nil {
				continue
			}
			switch {
			case m.Type == "system" && m.Subtype == "init":
				p.model = m.Model
			case m.Type == "result":
				return &TurnResult{Raw: line, Model: p.model}, nil
			}
		case <-deadline:
			return nil, errors.New("timed out waiting for claude's reply")
		}
	}
}

func (p *Proc) deathError() error {
	<-p.dead
	return p.deadErr
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
