---
description: Use when searching, analyzing, or understanding Claude Code session files. Provides the JSONL schema, file location conventions, and battle-tested jq patterns for extracting conversations, tool uses, and events from session transcripts.
---

# Claude Code Session Files

## File Locations

Session files live under `~/.claude/projects/`. Each project gets a directory named by replacing `/` with `-` in the absolute working directory path:

```
~/.claude/projects/-Users-chriswaddell-spare/          # /Users/chriswaddell/spare
~/.claude/projects/-Users-chriswaddell-chriswa-devkit/  # /Users/chriswaddell/chriswa-devkit
```

Within each project directory:
- `{session-id}.jsonl` - Main session transcript
- `{session-id}/subagents/agent-*.jsonl` - Subagent (Task tool) transcripts
- `sessions-index.json` - Session index
- `memory/` - Auto-memory files

To construct the path for the current session:
```bash
SESSION_FILE=~/.claude/projects/$(echo "$CLAUDE_CWD" | tr '/' '-')/${CLAUDE_SESSION_ID}.jsonl
```

## JSONL Schema

Each line is a JSON object with a `type` field. Common fields on most event types:

| Field | Description |
|-------|-------------|
| `type` | Event type (see below) |
| `timestamp` | ISO 8601 timestamp |
| `uuid` | Unique event ID |
| `parentUuid` | Parent event UUID (conversation threading) |
| `sessionId` | Session UUID |
| `cwd` | Working directory |
| `slug` | Human-readable session name |
| `gitBranch` | Active git branch |
| `version` | Claude Code version |

### Event Types

**`user`** - User messages and tool results
- Human-typed message: `message.content` is a **string**
- Tool result: `message.content` is an **array** containing `tool_result` blocks
- Has `toolUseResult` field on tool results (e.g. "User rejected tool use")

**`assistant`** - Agent responses
- `message.content` is always an **array** of content blocks:
  - `{type: "text", text: "..."}` - Text output to user
  - `{type: "tool_use", name: "Read", id: "toolu_...", input: {...}}` - Tool invocation
- `message.model` - Model used
- `message.stop_reason` - Why generation stopped
- `message.usage` - Token counts

**`summary`** - Session summary (can appear multiple times, last one is most current)
- `summary` - Summary text string
- `leafUuid` - UUID of the last message when summary was generated

**`system`** - System events
- `subtype` - e.g. `"turn_duration"`
- `durationMs` - Duration in milliseconds

**`progress`** - Progress updates (hooks, agents, searches)
- `data.type` - e.g. `"hook_progress"`, `"agent_progress"`, `"query_update"`
- `data.hookName` - Hook name for hook progress events

**`file-history-snapshot`** - File backup snapshots (no timestamp field)
- `snapshot.trackedFileBackups` - Map of backed-up files

## jq Patterns

### Identifying Human vs Tool Messages

The critical distinction: human-typed messages have `message.content` as a **string**, while tool results have it as an **array**.

```bash
# Human-typed messages only
jq -r 'select(.type == "user" and (.message.content | type) == "string")'

# Tool results only
jq -r 'select(.type == "user" and (.message.content | type) == "array")'
```

### Session Metadata (single pass)

```bash
jq -s '{
  summary: ([.[] | select(.type == "summary") | .summary] | last // "(no summary)"),
  first_ts: ([.[] | select(.timestamp) | .timestamp] | sort | first),
  last_ts: ([.[] | select(.timestamp) | .timestamp] | sort | last),
  cwd: ([.[] | select(.cwd) | .cwd] | first),
  session_id: ([.[] | select(.sessionId) | .sessionId] | first),
  slug: ([.[] | select(.slug) | .slug] | first),
  human_msgs: [.[] | select(.type == "user" and (.message.content | type) == "string")] | length,
  assistant_msgs: [.[] | select(.type == "assistant")] | length,
  tool_uses: [.[] | select(.type == "assistant") | .message.content[]? | select(.type == "tool_use")] | length,
  event_types: (group_by(.type) | map({key: first.type, value: length}) | from_entries)
}' "$SESSION"
```

### Conversation Flow (human + agent text only)

```bash
jq -r '
  if .type == "user" and (.message.content | type) == "string" then
    "[\(.timestamp[11:19])] USER: \(.message.content[0:120])"
  elif .type == "assistant" then
    ([.message.content[]? | select(.type == "text") | .text] | join(" ") | .[0:120]) as $text |
    if ($text | length) > 0 then "[\(.timestamp[11:19])] AGENT: \($text)" else empty end
  else empty end
' "$SESSION"
```

### Text Search Across Messages

```bash
jq -r --arg q "search term" '
  (
    if .type == "user" and (.message.content | type) == "string" then
      {role: "USER", text: .message.content, ts: .timestamp}
    elif .type == "assistant" then
      ([.message.content[]? | select(.type == "text") | .text] | join(" ")) as $text |
      if ($text | length) > 0 then {role: "AGENT", text: $text, ts: .timestamp}
      else empty end
    else empty end
  ) |
  select(.text | ascii_downcase | contains($q | ascii_downcase)) |
  "[\(.ts[11:19])] \(.role): \(.text[0:150])"
' "$SESSION"
```

### Events in a Time Range

```bash
jq -r '
  select(.timestamp >= "2026-02-02T18:31:00" and .timestamp <= "2026-02-02T18:35:00") |
  if .type == "user" and (.message.content | type) == "string" then
    "[\(.timestamp[11:19])] USER: \(.message.content[0:120])"
  elif .type == "user" then
    "[\(.timestamp[11:19])] TOOL_RESULT"
  elif .type == "assistant" then
    ([.message.content[]? |
      if .type == "text" then "text: \(.text[0:80])"
      elif .type == "tool_use" then "tool: \(.name)"
      else empty end] | join(" | ")) as $detail |
    "[\(.timestamp[11:19])] AGENT: \($detail)"
  elif .type == "system" then
    "[\(.timestamp[11:19])] SYSTEM: \(.subtype)"
  elif .type == "progress" then
    "[\(.timestamp[11:19])] PROGRESS: \(.data.type // "?")"
  else
    "[\(.timestamp[11:19])] \(.type)"
  end
' "$SESSION"
```

### Tool Use Details

```bash
jq -r '
  select(.type == "assistant") |
  .timestamp as $ts |
  .message.content[]? |
  select(.type == "tool_use") |
  "\($ts[11:19]) \(.name): \(
    if .name == "Read" then (.input.file_path // "?")
    elif .name == "Edit" then (.input.file_path // "?")
    elif .name == "Write" then (.input.file_path // "?")
    elif .name == "Bash" then (.input.command // "?" | .[0:80])
    elif .name == "Grep" then "pattern=\(.input.pattern // "?") path=\(.input.path // ".")"
    elif .name == "Glob" then (.input.pattern // "?")
    elif .name == "Task" then (.input.prompt // "?" | .[0:80])
    elif .name == "WebSearch" then (.input.query // "?" | .[0:80])
    elif .name == "WebFetch" then (.input.url // "?" | .[0:80])
    else (.input | tostring | .[0:80])
    end
  )"
' "$SESSION"
```

### Tool Use Frequency

```bash
jq -s '
  [.[] | select(.type == "assistant") | .message.content[]? | select(.type == "tool_use") | .name] |
  group_by(.) | map({tool: first, count: length}) | sort_by(-.count)
' "$SESSION"
```

### Files Edited in a Session

```bash
jq -s '
  [.[] | select(.type == "assistant") | .message.content[]? |
   select(.type == "tool_use" and (.name == "Edit" or .name == "Write")) |
   .input.file_path] | unique | .[]
' "$SESSION"
```

### Get Full Text of Nth Human Message (0-indexed)

```bash
jq -s --argjson n 0 '
  [.[] | select(.type == "user" and (.message.content | type) == "string")] |
  .[$n] | {timestamp, content: .message.content}
' "$SESSION"
```

## Tips

- Use `jq -s` (slurp) when you need to aggregate across all lines (sorting, grouping, counting). For streaming/filtering individual events, plain `jq` is more memory-efficient.
- ISO 8601 timestamps sort lexicographically, so string comparison works for time-range filters.
- Sessions with 0 human messages and only `file-history-snapshot` events are orphaned snapshots, not real conversations.
- Subagent files are named `agent-{hash}.jsonl` and have the same schema but include an `agentId` field.
