---
description: Use when spawning a Claude Code child process from inside a Claude Code session — headless (-p/stream-json) or interactive (pty). Covers the inherited env var that silently breaks nested interactive sessions.
---

# Spawning Claude Code from inside Claude Code

## The trap — read this first

A Claude Code session exports these into **every** child process:

```
AI_AGENT=claude-code_<version>_agent    CLAUDE_CODE_ENTRYPOINT=cli
CLAUDECODE=1                            CLAUDE_CODE_EXECPATH=…
CLAUDE_CODE_CHILD_SESSION=1             CLAUDE_CODE_MESSAGING_SOCKET=/tmp/cc-socks/<pid>.sock
CLAUDE_CODE_SESSION_ID=<uuid>           CLAUDE_CODE_MESSAGING_TOKEN=…
CLAUDE_CODE_BRIDGE_SESSION_ID=…         CLAUDE_EFFORT=…   CLAUDE_PID=…
```

**`CLAUDE_CODE_CHILD_SESSION=1` silently breaks a nested *interactive* session.**
It boots, renders its TUI, accepts the trust dialog — then never completes a
turn and never writes a transcript. It looks like your pty harness is broken.

Bisect (fresh pty session, one prompt, did any assistant record reach the
transcript?):

| env kept (rest scrubbed) | turn completed |
|---|---|
| *nothing — fully scrubbed* | yes |
| `CLAUDECODE` | yes |
| `CLAUDE_CODE_SESSION_ID` | yes |
| `CLAUDE_CODE_MESSAGING_SOCKET` + `_TOKEN` | yes |
| `CLAUDE_CODE_ENTRYPOINT` | yes |
| **`CLAUDE_CODE_CHILD_SESSION`** | **no** |

Headless (`-p`) children are **not** affected — they work with the env fully
contaminated. This only bites nested interactive sessions.

### Always scrub

```python
env = {k: v for k, v in os.environ.items()
       if not k.startswith(("CLAUDE", "ANTHROPIC"))
       and k not in {"CLAUDECODE", "AI_AGENT"}}
env["TERM"] = "xterm-256color"
```

```bash
env -u CLAUDECODE -u AI_AGENT -u CLAUDE_CODE_CHILD_SESSION \
    -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_EXECPATH \
    -u CLAUDE_CODE_MESSAGING_SOCKET -u CLAUDE_CODE_MESSAGING_TOKEN \
    -u CLAUDE_CODE_BRIDGE_SESSION_ID -u CLAUDE_EFFORT -u CLAUDE_PID \
    claude …
```

Scrub the whole prefix, not just the one variable — it is what the official
Agent SDK does for its own children, and it survives the next variable they add.

## Headless child (preferred)

```bash
claude -p --verbose --input-format stream-json --output-format stream-json
```

- `--verbose` is **mandatory** with `-p --output-format stream-json`; without it
  the CLI exits with an error.
- stdin takes one JSON object per line and stays open for multi-turn:
  `{"type":"user","message":{"role":"user","content":"…"}}`
- Add `--no-session-persistence` for throwaway runs, `--session-id <uuid>` to
  control the transcript path.

stdout event types: `system`/`init` (carries `session_id`, `cwd`, the full
`tools` array), `system`/`thinking_tokens`, `rate_limit_event`, `assistant`
(blocks: `thinking` / `text` / `tool_use`), `user` (`tool_result` blocks plus a
structured `tool_use_result` sidecar), `result` (`total_cost_usd`, usage,
`duration_api_ms`).

Render tool output from the `tool_use_result` sidecar, not the flattened
`tool_result` block — the sidecar has `stdout`/`stderr`/`isImage`/diff data.

## Interactive child (pty) — only when you need TUI-only behaviour

Needs the env scrub above, plus three non-obvious fixes:

```python
import os, pty, time, uuid, glob, select, re

SID = str(uuid.uuid4()); WORK = "/tmp/ptywork"; os.makedirs(WORK, exist_ok=True)
env = {k: v for k, v in os.environ.items()
       if not k.startswith(("CLAUDE", "ANTHROPIC"))
       and k not in {"CLAUDECODE", "AI_AGENT"}}
env["TERM"] = "xterm-256color"

pid, fd = pty.fork()
if pid == 0:
    os.chdir(WORK)
    os.execvpe("claude", ["claude", "--session-id", SID, "--model", "haiku",
                          "--permission-mode", "bypassPermissions"], env)
    os._exit(1)

buf = b""
ANSI = re.compile(rb'\x1b\[[0-9;?]*[a-zA-Z]|\x1b[\]P][^\x07\x1b]*(\x07|\x1b\\)?')
screen = lambda: ANSI.sub(b'', buf).decode('utf8', 'replace')

def pump(d):
    global buf
    end = time.time() + d
    while time.time() < end:
        if select.select([fd], [], [], 0.2)[0]:
            try: buf += os.read(fd, 65536)
            except OSError: return

pump(8)
if 'trust' in screen():                    # trust dialog — match ONE word
    os.write(fd, b"\x1b[B"); time.sleep(0.6); os.write(fd, b"\r"); pump(6)

os.write(fd, b"Reply with only the token made by joining ZEB and RAFF, no separator.")
time.sleep(1.2); os.write(fd, b"\r")       # type, pause, THEN Enter

F = None
for i in range(15):
    pump(2)
    if not F:
        g = glob.glob(os.path.expanduser("~/.claude/projects/*/%s.jsonl" % SID))
        F = g[0] if g else None
    n = sum(1 for l in open(F) if '"type":"assistant"' in l) if F and os.path.exists(F) else 0
    print(f"t={i*2}s file={'y' if F else 'n'} records={n} screen={'ZEBRAFF' in screen()}")
os.kill(pid, 15)
```

1. **Workspace trust dialog** blocks the first run in any new directory. Its text
   is interleaved with cursor escapes
   (`Yes,\x1b[9GI\x1b[11Gtrust\x1b[17Gthis\x1b[22Gfolder`), so
   `b"trust this folder" in buf` never matches. Strip ANSI, or match one
   contiguous word. Answer with `\x1b[B` then `\r`.
2. **Never screen-scrape for a marker that also appears in your prompt** — the
   input box echoes what you typed, so you match your own text and get a false
   positive. Use a marker the *model constructs* (`ZEB` + `RAFF` → `ZEBRAFF`).
3. **Turn-completed signal** = a transcript file exists with ≥1 assistant record.
   Screen state is not reliable.

Before believing any pty result, run the null-hypothesis control: does the
harness complete a plain turn with the feature under test absent? A broken
harness looks exactly like a real finding.

## Watching a child's transcript

- Transcript path: `~/.claude/projects/<cwd with / → ->/<session-id>.jsonl`.
  The leading `-` breaks naive globs — use `./*/*.jsonl`, not `*/*.jsonl`.
- One API response becomes **several** JSONL lines, one per content block,
  sharing `message.id` and ordered by `apiBlockIndex`. Group by `message.id`
  before rendering or a text block and its sibling tool call look unrelated.
- **`AskUserQuestion` defers the whole turn's flush.** While a question is
  pending, its preceding text, the `tool_use` and the `tool_result` are all
  absent from the transcript — visible only in the pty — and land together the
  moment it is answered. An ordinary blocking tool (35 s Bash) flushes its text
  within ~1 s, so this is specific to user-answered tools.
- Flushed records carry their **original creation timestamps**, not flush time,
  so you cannot detect the deferral from `timestamp`. Use file offset / arrival
  order for liveness.
- `AskUserQuestion` is **not available** in `-p`/stream-json mode at all (absent
  from the init `tools` list; `--tools` will not add it). A headless child cannot
  ask questions.

For live session discovery without spawning, see `~/.claude/sessions/<pid>.json`
(carries `cwd`, `status`, `messagingSocketPath`) and `/tmp/cc-socks/<pid>.sock`.
