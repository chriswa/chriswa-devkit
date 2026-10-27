#!/usr/bin/env bun

// sfltool Guard: every sfltool call pops a macOS admin-password dialog, so require the human's sign-off

const input = JSON.parse(await Bun.stdin.text())
const command = (input.tool_input?.command ?? '').trim()

const usesSfltool = /\bsfltool\b/.test(command)
const isApproved = /^HUMAN_SFLTOOL_APPROVAL=1\s/.test(command)
if (!usesSfltool || isApproved) {
  console.log(JSON.stringify({ hookSpecificOutput: { hookEventName: 'PreToolUse' }, continue: true, suppressOutput: true }))
  process.exit(0)
}

const reason = "sfltool Guard: sfltool pops a macOS administrator-password dialog on the user's screen. Don't use it unless you absolutely need to. If you do, get the user's explicit authorization first, then rerun the same command prefixed with `HUMAN_SFLTOOL_APPROVAL=1 `."
console.log(JSON.stringify({
  hookSpecificOutput: {
    hookEventName: 'PreToolUse',
    permissionDecision: 'deny',
    permissionDecisionReason: reason,
  },
  systemMessage: reason,
}))
