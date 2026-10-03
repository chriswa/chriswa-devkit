# claude-print-daemon

Keeps `claude -p` processes started and waiting, so a reply costs about the
API's own latency instead of Claude Code's ~1 s startup on every call. It uses
the unmodified Claude Code binary signed in with your own subscription.

```
go build -o claude-print-daemon .
echo "Name a colour." | ./claude-print-daemon ask --tag me --no-thinking
./claude-print-daemon ask -s <session_id> prompt.txt   # continue that session
./claude-print-daemon status                           # spares, live sessions, cost by tag
```

`ask` starts the daemon if it isn't running and prints the JSON response.
Prompts are read from a file or stdin, never from the command line.

## What is kept running

- **Spares:** one waiting process per profile. A profile is model, effort,
  system prompt and thinking on/off, which are all fixed when the process
  starts. Two profiles always have a spare, both at medium effort with an empty
  system prompt: Haiku with thinking off (`--no-thinking`) and Sonnet with
  thinking on. Any other profile gets one after its first request, and loses
  it after 15 minutes without requests. A claimed spare is replaced at once. A
  spare older than 15 minutes is replaced by a fresh process, which retires the
  old one only once it is ready, so a Claude Code update reaches the next call.
- **Live sessions:** after a turn, the session's process stays up for 15
  minutes, up to 5 sessions, evicting the least recently used. Continuing a
  live session skips startup entirely. Continuing one that has been shut down
  resumes it with `--resume`.

## Behaviour

- Every process runs with `--tools "" --strict-mcp-config --setting-sources ""
  --disable-slash-commands` and `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`.
  Your settings, hooks, CLAUDE.md files and MCP servers are not loaded.
- `--no-thinking` sets `MAX_THINKING_TOKENS=0`. Leave it off and Claude Code
  lets Haiku think, which adds about 0.4 s to a short reply.
- Continuing a session with a different model switches it in place. A
  different effort or thinking setting restarts the process with `--resume`.
  The system prompt can't be changed once a session has started.
- One turn at a time per session: a second request for a busy session gets 409.

## Files (`~/.claude-print-daemon`, or `$CPD_HOME`)

- `daemon.sock` — HTTP over a Unix socket: `POST /v1/ask`, `GET /v1/status`, `POST /v1/stop`
- `usage.jsonl` — one line per turn: tag, session, model, `total_cost_usd`, token usage
- `daemon.log` — output from a daemon started by `ask`
- `work/` — the working directory of every claude process. Transcripts land in
  `~/.claude/projects/` under this directory's name, which is how `--resume`
  finds them.

## POST /v1/ask

```json
{"prompt": "…", "session_id": "optional", "model": "haiku", "effort": "medium",
 "system_prompt": "", "no_thinking": false, "tag": "summary-chat"}
```

The response includes `session_id`, `result`, `source` (`spare`, `live`,
`cold` or `resume`), `model`, `total_cost_usd`, `usage` and `wall_ms`.
