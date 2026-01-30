#!/usr/bin/env bun

// Grep Guard: Block recursive grep commands that don't exclude node_modules (causes excessive delays)

const input = JSON.parse(await Bun.stdin.text())
const command = (input.tool_input?.command ?? '').trim()

// Only applies to recursive grep commands
const isRecursiveGrep = /^grep\s/.test(command) && /\s-[a-zA-Z]*r|--recursive/.test(command)
if (!isRecursiveGrep) {
  console.log(JSON.stringify({ hookSpecificOutput: { hookEventName: 'PreToolUse' }, continue: true, suppressOutput: true }))
  process.exit(0)
}

// If node_modules is mentioned (excluded), allow it
if (command.includes('node_modules')) {
  console.log(JSON.stringify({ hookSpecificOutput: { hookEventName: 'PreToolUse' }, continue: true, suppressOutput: true }))
  process.exit(0)
}

// Recursive grep without node_modules exclusion
const reason = 'Grep Guard: To avoid excessive delays, do not use `grep -r` without excluding `node_modules`'
console.log(JSON.stringify({
  hookSpecificOutput: {
    hookEventName: 'PreToolUse',
    permissionDecision: 'deny',
    permissionDecisionReason: reason,
  },
  systemMessage: reason,
}))
