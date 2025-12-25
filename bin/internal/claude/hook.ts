#!/usr/bin/env bun

// Claude Code Hook Handler
// Purpose: Generic hook handler that loads rules from a configurable directory
// Usage: claude-hook.ts <toolName> <hookType>
// Example: claude-hook.ts bash pretooluse

import { writeFileSync } from 'fs'
import { homedir } from 'os'
import { join } from 'path'
import { z } from 'zod'
import type { Rule, RuleContext, RuleDecision } from './bash/pretooluse/types'

// Parse CLI arguments
const toolName = Bun.argv[2]?.toLowerCase()
const hookType = Bun.argv[3]?.toLowerCase()

if (!toolName || !hookType) {
  console.error('Usage: claude-hook.ts <toolName> <hookType>')
  console.error('Example: claude-hook.ts bash pretooluse')
  process.exit(1)
}

// Dynamically load all rules from the index file
// Using absolute path based on script location to work from any CWD
const indexPath = join(import.meta.dir, toolName, hookType, 'index.ts')

let rules: Rule[] = []
try {
  const rulesModule = await import(indexPath)
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

// Type for hook output
interface HookOutput {
  hookSpecificOutput: {
    hookEventName: 'PreToolUse'
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
function outputHookResponse(decision: 'allow' | 'deny' | 'ask', reason: string): void {
  const response: HookOutput = {
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      permissionDecision: decision,
      permissionDecisionReason: reason,
    },
  }
  console.log(JSON.stringify(response))
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
const decisions: RuleDecision[] = rules
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
