#!/usr/bin/env bun

// Find Guard: Block find commands that don't exclude node_modules (causes excessive delays)

const input = JSON.parse(await Bun.stdin.text())
const command = (input.tool_input?.command ?? '').trim()

// Only applies to find commands
if (!/^find\s/.test(command)) {
  process.exit(0) // No opinion
}

// If node_modules is mentioned (excluded), allow it
if (command.includes('node_modules')) {
  process.exit(0) // No opinion - exclusion present
}

// Find command without node_modules exclusion
const reason = 'To avoid excessive delays, do not use `find` without excluding `node_modules`'
console.log(JSON.stringify({
  hookSpecificOutput: {
    hookEventName: 'PreToolUse',
    permissionDecision: 'deny',
    permissionDecisionReason: reason,
  },
  systemMessage: reason,
}))
