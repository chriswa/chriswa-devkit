---
description: Use when the user asks about something discussed earlier in this session that you don't have access to. This typically happens after context compaction removes earlier conversation history. Allows searching the raw session transcript file to recover lost context.
---

# Session Memory Recall

This skill helps you access information from earlier in the current Claude Code session that may have been removed due to context compaction.

## When to Use This

- User references something "we discussed earlier" that you have no record of
- User asks you to recall a decision, code snippet, or conversation from earlier in the session
- You suspect context compaction has removed relevant earlier messages

## Finding the Session File

The current session's transcript is stored as a JSONL file. You can construct the path using environment variables:

```bash
# These env vars are set by the SessionStart hook:
echo "Session ID: $CLAUDE_SESSION_ID"
echo "Working directory: $CLAUDE_CWD"

# The session file path is:
SESSION_FILE=~/.claude/projects/$(echo "$CLAUDE_CWD" | tr '/' '-')/${CLAUDE_SESSION_ID}.jsonl

# Verify it exists:
ls -la "$SESSION_FILE"
```

## Quick Search with Grep

For simple lookups, grep with context often works well:

```bash
SESSION_FILE=~/.claude/projects/$(echo "$CLAUDE_CWD" | tr '/' '-')/${CLAUDE_SESSION_ID}.jsonl

# Search for a keyword with context
grep -i "search_term" "$SESSION_FILE" | head -20

# For more readable output, extract just the text content:
grep -i "search_term" "$SESSION_FILE" | jq -r '.message.content // .message.content[0].text // empty' 2>/dev/null | head -50
```

## Understanding the Session File Format

For the full JSONL schema, jq patterns, and advanced analysis techniques, load the `claude-code-session-files` skill. A quick summary:

- `"type": "user"` with `message.content` as **string** = human-typed message
- `"type": "user"` with `message.content` as **array** = tool result
- `"type": "assistant"` with content blocks of `type: "text"` = agent text output
- `"type": "assistant"` with content blocks of `type: "tool_use"` = tool invocations
- `"type": "summary"` = session summary

## Recommendation: Use a Sub-Agent

**Important:** To avoid wasting context in the main conversation, delegate this search to a sub-agent:

```
Use the Task tool with subagent_type="Explore" to search the session file for [specific information the user asked about]. The session file is at: ~/.claude/projects/[constructed-path]/${CLAUDE_SESSION_ID}.jsonl
```

The sub-agent can grep, parse, and summarize findings without bloating the main conversation context.
