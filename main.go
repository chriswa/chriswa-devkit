// claude-print-daemon keeps `claude -p` processes started and waiting, so a
// caller gets a reply in roughly the API's own latency instead of paying
// Claude Code's startup on every request.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultModel  = "haiku"
	defaultEffort = "medium"
)

type paths struct {
	home, socket, work, log, usage string
}

func resolvePaths() paths {
	home := os.Getenv("CPD_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".claude-print-daemon")
	}
	return paths{
		home:   home,
		socket: filepath.Join(home, "daemon.sock"),
		work:   filepath.Join(home, "work"),
		log:    filepath.Join(home, "daemon.log"),
		usage:  filepath.Join(home, "usage.jsonl"),
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "ask":
		err = ask(os.Args[2:])
	case "status":
		err = status()
	case "stop":
		err = stopDaemon()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "claude-print-daemon:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  claude-print-daemon ask [flags] [prompt-file]   send a prompt (stdin if no file or "-")
      -s <session-id>      continue a session
      -m <model>           model alias or id (default haiku)
      -e <effort>          effort level (default medium)
      --system-file <f>    system prompt for a new session (default empty)
      --no-thinking        disable extended thinking (MAX_THINKING_TOKENS=0)
      --tag <name>         caller name recorded in the usage log
  claude-print-daemon status                      spares, live sessions, cost totals
  claude-print-daemon serve                       run the daemon in the foreground
  claude-print-daemon stop                        shut the daemon down
`)
}

// ---- daemon ----

// askBody is the wire format of POST /v1/ask. It is flat so that callers do
// not need to know which fields are process flags.
type askBody struct {
	Prompt       string `json:"prompt"`
	SessionID    string `json:"session_id,omitempty"`
	Model        string `json:"model,omitempty"`
	Effort       string `json:"effort,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	NoThinking   bool   `json:"no_thinking,omitempty"`
	Tag          string `json:"tag,omitempty"`
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	claudeBin := fs.String("claude", envOr("CPD_CLAUDE_BIN", "claude"), "claude binary")
	_ = fs.Parse(args)

	pt := resolvePaths()
	for _, d := range []string{pt.home, pt.work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if c, err := net.Dial("unix", pt.socket); err == nil {
		c.Close()
		return errors.New("already running at " + pt.socket)
	}
	_ = os.Remove(pt.socket)
	ln, err := net.Listen("unix", pt.socket)
	if err != nil {
		return err
	}
	_ = os.Chmod(pt.socket, 0o600)

	logf, err := os.OpenFile(pt.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	// Always to the file, so the trail is the same however the daemon was
	// started; to the terminal as well when run by hand.
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		log.SetOutput(io.MultiWriter(logf, os.Stderr))
	} else {
		log.SetOutput(logf)
	}

	bin, err := exec.LookPath(*claudeBin)
	if err != nil {
		return fmt.Errorf("finding claude: %w", err)
	}
	ul := &usageLog{path: pt.usage}
	pool := NewPool(Config{
		ClaudeBin:      bin,
		Dir:            pt.work,
		SpareTTL:       15 * time.Minute,
		IdleTTL:        15 * time.Minute,
		MaxLive:        5,
		ProfileIdleTTL: 15 * time.Minute,
		StartTimeout:   30 * time.Second,
		TurnTimeout:    10 * time.Minute,
		TickInterval:   5 * time.Second,
		RetryBackoff:   30 * time.Second,
		// Haiku is the model asked for when a reply must be quick, and
		// thinking roughly doubles its latency on a short answer.
		Defaults: []Profile{
			{Model: "haiku", Effort: defaultEffort, NoThinking: true},
			{Model: "sonnet", Effort: defaultEffort},
		},
		OnResult: ul.append,
	})
	log.Printf("listening on %s (claude: %s)", pt.socket, bin)

	srv := &http.Server{Handler: handler(pool, ul)}
	mux := srv.Handler.(*http.ServeMux)
	mux.HandleFunc("POST /v1/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		go func() { _ = srv.Shutdown(context.Background()) }()
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		_ = srv.Shutdown(context.Background())
	}()
	err = srv.Serve(ln)
	pool.Close()
	_ = os.Remove(pt.socket)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func handler(pool *Pool, ul *usageLog) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/ask", func(w http.ResponseWriter, r *http.Request) {
		var b askBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(b.Prompt) == "" {
			httpError(w, http.StatusBadRequest, errors.New("prompt is empty"))
			return
		}
		if b.Model == "" {
			b.Model = defaultModel
		}
		if b.Effort == "" {
			b.Effort = defaultEffort
		}
		started := time.Now()
		log.Printf("ask tag=%s session=%s model=%s no_thinking=%v prompt=%d chars", orNone(b.Tag), orNone(b.SessionID), b.Model, b.NoThinking, len(b.Prompt))
		resp, err := pool.Ask(AskRequest{
			Prompt:    b.Prompt,
			SessionID: b.SessionID,
			Tag:       b.Tag,
			Profile:   Profile{Model: b.Model, Effort: b.Effort, SystemPrompt: b.SystemPrompt, NoThinking: b.NoThinking},
		})
		if err != nil {
			log.Printf("ask tag=%s FAILED after %dms: %v", orNone(b.Tag), time.Since(started).Milliseconds(), err)
			ul.appendFailure(b, err)
			code := http.StatusBadGateway
			if errors.Is(err, ErrBusy) {
				code = http.StatusConflict
			}
			httpError(w, code, err)
			return
		}
		log.Printf("ask tag=%s done: session=%s source=%s model=%s wall=%dms cost=$%.4f is_error=%v",
			orNone(b.Tag), resp.SessionID, resp.Source, resp.Model, resp.WallMS, resp.TotalCostUSD, resp.IsError)
		if resp.IsError {
			log.Printf("ask tag=%s claude reported an error (%s): %s", orNone(b.Tag), resp.Subtype, resp.Result)
		}
		writeJSON(w, resp)
	})
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"pool": pool.Status(), "cost": ul.totals()})
	})
	return mux
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// usageLog appends one line per turn, so cost can be reviewed per caller.
type usageLog struct {
	mu   sync.Mutex
	path string
}

type usageEntry struct {
	Time time.Time `json:"time"`
	// Error is set, and nothing was billed, when the turn never completed.
	Error        string          `json:"error,omitempty"`
	Tag          string          `json:"tag,omitempty"`
	SessionID    string          `json:"session_id"`
	Source       Source          `json:"source"`
	Model        string          `json:"model"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	WallMS       int64           `json:"wall_ms"`
	Usage        json.RawMessage `json:"usage,omitempty"`
}

func (u *usageLog) append(r *AskResponse, tag string) {
	e := usageEntry{
		Time: time.Now(), Tag: tag, SessionID: r.SessionID, Source: r.Source,
		Model: r.Model, TotalCostUSD: r.TotalCostUSD, WallMS: r.WallMS, Usage: r.Usage,
	}
	if r.IsError {
		e.Error = r.Result
	}
	u.write(e)
}

func (u *usageLog) appendFailure(b askBody, err error) {
	u.write(usageEntry{Time: time.Now(), Tag: b.Tag, SessionID: b.SessionID, Model: b.Model, Error: err.Error()})
}

func (u *usageLog) write(e usageEntry) {
	b, _ := json.Marshal(e)
	u.mu.Lock()
	defer u.mu.Unlock()
	f, err := os.OpenFile(u.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("usage log: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

type costTotals struct {
	Turns   int     `json:"turns"`
	CostUSD float64 `json:"cost_usd"`
}

func (u *usageLog) totals() map[string]map[string]costTotals {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := map[string]map[string]costTotals{"all_time": {}, "last_24h": {}}
	f, err := os.Open(u.path)
	if err != nil {
		return out
	}
	defer f.Close()
	cutoff := time.Now().Add(-24 * time.Hour)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var e usageEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		tag := e.Tag
		if tag == "" {
			tag = "(untagged)"
		}
		for _, window := range []string{"all_time", "last_24h"} {
			if window == "last_24h" && e.Time.Before(cutoff) {
				continue
			}
			for _, k := range []string{tag, "total"} {
				t := out[window][k]
				t.Turns++
				t.CostUSD += e.TotalCostUSD
				out[window][k] = t
			}
		}
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---- client ----

func client(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
}

// ensureDaemon starts the daemon in the background if nothing is listening.
func ensureDaemon(pt paths) error {
	if c, err := net.Dial("unix", pt.socket); err == nil {
		c.Close()
		return nil
	}
	if err := os.MkdirAll(pt.home, 0o700); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// The daemon logs to this file itself; its stderr goes here too so that
	// a panic is not lost.
	logf, err := os.OpenFile(pt.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(self, "serve")
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if c, err := net.Dial("unix", pt.socket); err == nil {
			c.Close()
			return nil
		}
	}
	return errors.New("daemon did not start; see " + pt.log)
}

func ask(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	session := fs.String("s", "", "session id to continue")
	model := fs.String("m", "", "model")
	effort := fs.String("e", "", "effort")
	systemFile := fs.String("system-file", "", "system prompt file")
	noThinking := fs.Bool("no-thinking", false, "disable extended thinking")
	tag := fs.String("tag", "", "caller name for the usage log")
	_ = fs.Parse(args)

	var prompt []byte
	var err error
	if f := fs.Arg(0); f != "" && f != "-" {
		prompt, err = os.ReadFile(f)
	} else {
		prompt, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		return err
	}
	body := askBody{Prompt: string(prompt), SessionID: *session, Model: *model, Effort: *effort, NoThinking: *noThinking, Tag: *tag}
	if *systemFile != "" {
		sp, err := os.ReadFile(*systemFile)
		if err != nil {
			return err
		}
		body.SystemPrompt = string(sp)
	}

	pt := resolvePaths()
	if err := ensureDaemon(pt); err != nil {
		return err
	}
	b, _ := json.Marshal(body)
	resp, err := client(pt.socket).Post("http://cpd/v1/ask", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(resp.Body)
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("daemon returned %s: %s", resp.Status, e.Error)
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func status() error {
	pt := resolvePaths()
	resp, err := client(pt.socket).Get("http://cpd/v1/status")
	if err != nil {
		return fmt.Errorf("daemon not running? %w", err)
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func stopDaemon() error {
	pt := resolvePaths()
	resp, err := client(pt.socket).Post("http://cpd/v1/stop", "", nil)
	if err != nil {
		return fmt.Errorf("daemon not running? %w", err)
	}
	resp.Body.Close()
	return nil
}
