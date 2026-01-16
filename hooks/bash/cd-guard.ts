#!/usr/bin/env bun

// CD Guard: Block bare cd commands - they should be wrapped in a subshell
// Example: (cd subdir && command) instead of cd subdir

const input = JSON.parse(await Bun.stdin.text())
const command = (input.tool_input?.command ?? '').trim()

// Only applies to cd commands
if (!/^cd(\s|$)/.test(command)) {
  process.exit(0) // No opinion
}

// Block cd commands
const reason = 'CD Guard: cd commands should be wrapped in a subshell: (cd subdir && command)'
console.log(JSON.stringify({
  hookSpecificOutput: {
    hookEventName: 'PreToolUse',
    permissionDecision: 'deny',
    permissionDecisionReason: reason,
  },
  systemMessage: reason,
}))
