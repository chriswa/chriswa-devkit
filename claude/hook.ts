#!/usr/bin/env bun

// Claude Code Hook Handler
// Purpose: Generic hook handler that loads rules from a configurable directory
// Usage: hook.ts <toolName> <hookEventName>
// Example: hook.ts bash PreToolUse

import { Rule, RuleContext, RuleDecision } from './types'
import { writeFileSync } from 'fs'
import { homedir } from 'os'
import { join } from 'path'
import { z } from 'zod'

// Parse CLI arguments
const toolName = Bun.argv[2]?.toLowerCase()
const hookEventName = Bun.argv[3]

if (!toolName || !hookEventName) {
  console.error('Usage: hook.ts <toolName> <hookEventName>')
  console.error('Example: hook.ts bash PreToolUse')
  process.exit(1)
}

// Complete list of all valid Claude Code hook event names
const validHookEventNames = new Set([
  'PreToolUse',
  'PostToolUse',
  'UserPromptSubmit',
  'PermissionRequest',
  'Notification',
  'Stop',
  'SubagentStop',
  'PreCompact',
  'SessionStart',
  'SessionEnd',
])

if (!validHookEventNames.has(hookEventName)) {
  console.error(`Unknown hook event name: ${hookEventName}`)
  console.error(`Valid hook event names: ${Array.from(validHookEventNames).join(', ')}`)
  process.exit(1)
}

// Dynamically load all rules from the index file based on hook type and tool name
// The hook.ts file is now in claude/, and rules are in claude/hooks/{hookEventName}/{toolName}/
const rulesDir = join(import.meta.dir, 'hooks', hookEventName.toLowerCase(), toolName)
const indexPath = join(rulesDir, 'index.ts')

let rules: Array<Rule> = []
try {
  const rulesModule = await import(indexPath) as Record<string, unknown>
  // Extract all exported rule functions (excludes type exports)
  rules = Object.values(rulesModule).filter(
    (exp): exp is Rule => typeof exp === 'function',
  )
}
catch (error) {
  console.error(`Error: Could not load rules from ${toolName}/${hookType}`)
  console.error(error)
  process.exit(1)
}

// Zod schema for hook input
const HookInputSchema = z.object({
  tool_name: z.string().optional(),
  tool_input: z.object({
    command: z.string().optional(),
  }).optional(),
})

// Type for hook output (correct format per Claude Code docs)
interface HookOutput {
  hookSpecificOutput: {
    hookEventName: 'PreToolUse' | 'PostToolUse' | 'UserPromptSubmit' | 'PermissionRequest' |
      'Notification' | 'Stop' | 'SubagentStop' | 'PreCompact' | 'SessionStart' | 'SessionEnd'
    permissionDecision: 'allow' | 'deny' | 'ask'
    permissionDecisionReason: string
  }
}

// Read the hook input from stdin
const input = await Bun.stdin.text()

// Save the input to a file for inspection (useful for debugging)
const debugFilePath = join(homedir(), '.claude.lasttool.json')
writeFileSync(debugFilePath, input)

// Parse and validate the JSON input
const parseResult = HookInputSchema.safeParse(JSON.parse(input))
if (!parseResult.success) {
  console.error('Invalid hook input:', parseResult.error)
  process.exit(1)
}

const data = parseResult.data

// Extract tool input
const actualToolName = data.tool_name ?? ''
const command = data.tool_input?.command ?? ''

// Helper function to output hook response
function outputHookResponse(permissionDecision: 'allow' | 'deny' | 'ask', permissionDecisionReason: string): void {
  const response: HookOutput = {
    hookSpecificOutput: {
      hookEventName,
      permissionDecision,
      permissionDecisionReason,
    },
  }
  process.stdout.write(`${JSON.stringify(response)}\n`)
}

// Only apply this hook to the specified tool
const expectedToolName = toolName.charAt(0).toUpperCase() + toolName.slice(1)
if (data.tool_name !== expectedToolName) {
  outputHookResponse('allow', `Not a ${expectedToolName} tool call`)
  process.exit(0)
}

// Build context for rules
const context: RuleContext = {
  toolName: actualToolName,
  command,
  normalizedCommand: command.trim(),
}

// Run ALL rules and collect decisions (filter out null responses)
const decisions: Array<RuleDecision> = rules
  .map((rule) => rule(context))
  .filter((result): result is RuleDecision => result !== null)

// If no rules had an opinion, allow the command
if (decisions.length === 0) {
  outputHookResponse('allow', 'Command allowed')
  process.exit(0)
}

// Find the highest priority decision
const highestPriority = decisions.reduce((max, curr) =>
  curr.priority > max.priority ? curr : max,
)

// Output the winning decision
outputHookResponse(highestPriority.decision, highestPriority.reason)
process.exit(0)
