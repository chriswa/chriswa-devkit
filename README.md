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

## One long-lived conversation

Four `ask` options keep a session cheap to continue for hours. Each applies
from that request until the session's next one, which replaces it: a request
that leaves an option out turns it off for the session.

- `--keep-alive <dur>` keeps the process live this long after the turn instead
  of 15 minutes, up to 60m (the prompt cache's lifetime).
- `--priority` exempts the session from the 5-session cap: others are evicted
  first, and if every live session is prioritized the cap gives way. It still
  ends when its keep-alive runs out.
- `--auto-compact` compacts the session 55 minutes after the turn, on the
  still-warm 1-hour cache, so the next turn starts from a small context rather
  than a cold read of a large one. A later request reschedules it from that
  request's end, or cancels it if it lacks the flag. Scheduled compactions are
  kept in `compactions.json` and survive a restart: on startup one is kept if
  it is not yet due, run at once if it is due and the last turn was under 59
  minutes ago, and dropped (with a log line) after that, since its cache may
  be cold.
- `--compact-above <tokens>` compacts right after the turn, in the
  background, if the turn's context exceeds the threshold.

A request for a session that is being compacted waits for the compaction to
finish. Compaction sends `/compact` to a separate process resumed on the
session without `--disable-slash-commands` (under that flag Claude Code
refuses it); the live process is then replaced by a fresh resumed one, so the
next turn is still `live`. The compaction's own API call (Claude Code
2.1.288) reuses the prompt cache only up to the end of the conversation's first
user message, so the system prompt and first message are read from cache and
everything after them is paid for uncached. The response's
`compaction.cache_read_tokens` and `input_tokens` show the split. Compaction cost is logged under the tag
`compact:<tag>`. `CPD_COMPACT_AFTER` and `CPD_COMPACT_LATEST` (durations, read
by `serve`) override the 55m and 59m for testing.

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
- `compactions.json` — scheduled `--auto-compact` compactions
- `daemon.log` — output from a daemon started by `ask`
- `work/` — the working directory of every claude process. Transcripts land in
  `~/.claude/projects/` under this directory's name, which is how `--resume`
  finds them.

## POST /v1/ask

```json
{"prompt": "…", "session_id": "optional", "model": "haiku", "effort": "medium",
 "system_prompt": "", "no_thinking": false, "tag": "summary-chat",
 "keep_alive": "60m", "priority": false, "auto_compact": false, "compact_above": 0}
```

The response includes `session_id`, `result`, `source` (`spare`, `live`,
`cold` or `resume`), `model`, `total_cost_usd`, `usage`, `wall_ms` and
`context_tokens` (the final API call's input + cache read + cache creation
tokens: the context the turn ended on). Two more appear when relevant:

- `compaction`: a compaction of this session that finished (or failed) since
  its previous turn — `trigger` (`auto` or `size`), `compacted_before_ms`,
  `waited_ms` (if this request waited for it), `context_tokens_before`,
  `context_tokens_after`, `duration_ms`, `cost_usd`, and `error` on failure.
- `next_compaction`: `{"trigger": "auto"|"size", "at": time}`, what this turn
  scheduled (`auto`) or started in the background (`size`).

`GET /v1/status` lists pending and running compactions under
`pool.compactions`, and each live session's `keep_alive_s` and `priority`.
