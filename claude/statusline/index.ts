#!/usr/bin/env bun

import { execSync } from 'child_process'
import { existsSync, readFileSync, writeFileSync } from 'fs'
import { homedir } from 'os'
import { join } from 'path'
import { z } from 'zod'

// Zod schema for status line input
const StatusLineInputSchema = z.object({
  model: z.object({
    display_name: z.string().optional(),
  }).optional(),
  workspace: z.object({
    current_dir: z.string().optional(),
    project_dir: z.string().optional(),
  }).optional(),
  context_window: z.object({
    current_usage: z.object({
      input_tokens: z.number().optional(),
      output_tokens: z.number().optional(),
      cache_creation_input_tokens: z.number().optional(),
      cache_read_input_tokens: z.number().optional(),
    }).optional(),
    context_window_size: z.number().optional(),
  }).optional(),
  cost: z.object({
    total_cost_usd: z.number().optional(),
  }).optional(),
  session_id: z.string().optional(),
})

type StatusLineInput = z.infer<typeof StatusLineInputSchema>

// State file schema for 5-hour window tracking
const WindowStateSchema = z.object({
  currentWindow: z.object({
    startTime: z.string(),
    endTime: z.string(),
  }).optional(),
})

type WindowState = z.infer<typeof WindowStateSchema>

// Helper: Round timestamp to the nearest hour (UTC)
function roundToHour(date: Date): Date {
  const utc = new Date(date.toISOString())
  return new Date(Date.UTC(utc.getUTCFullYear(), utc.getUTCMonth(), utc.getUTCDate(), utc.getUTCHours(), 0, 0, 0))
}

// Helper: Load or initialize window state
function loadWindowState(stateFilePath: string): WindowState {
  if (!existsSync(stateFilePath)) {
    return { currentWindow: undefined }
  }

  try {
    const content = readFileSync(stateFilePath, 'utf-8')
    const parsed = WindowStateSchema.safeParse(JSON.parse(content))
    return parsed.success ? parsed.data : { currentWindow: undefined }
  }
  catch {
    return { currentWindow: undefined }
  }
}

// Helper: Get or create current 5-hour window
function getCurrentWindow(state: WindowState): { startTime: string, endTime: string } {
  const now = new Date()

  // Check if current window is still valid
  if (state.currentWindow && now < new Date(state.currentWindow.endTime)) {
    return state.currentWindow
  }

  // Create new 5-hour window
  const startTime = roundToHour(now)
  const endTime = new Date(startTime.getTime() + 5 * 60 * 60 * 1000)

  return {
    startTime: startTime.toISOString(),
    endTime: endTime.toISOString(),
  }
}

// Helper: Calculate time until reset in "H:MM" format
function formatTimeUntilReset(endTime: string): string {
  const now = new Date()
  const end = new Date(endTime)
  const msUntilReset = Math.max(0, end.getTime() - now.getTime())

  const hours = Math.floor(msUntilReset / 3600000)
  const minutes = Math.floor((msUntilReset % 3600000) / 60000)

  return `${hours}:${minutes.toString().padStart(2, '0')}`
}

// Read input from stdin
const input = await Bun.stdin.text()

// Write input to ~/.claude.laststatusline.json
const statusFilePath = join(homedir(), '.claude.laststatusline.json')
writeFileSync(statusFilePath, input)

// Parse and validate JSON
const parseResult = StatusLineInputSchema.safeParse(JSON.parse(input))
if (!parseResult.success) {
  console.error('Invalid status line input:', parseResult.error)
  process.exit(1)
}

const data: StatusLineInput = parseResult.data

// Extract data with proper fallbacks
const model = data.model?.display_name ?? 'Unknown'
const currentDir = data.workspace?.current_dir ?? '/'
const projectDir = data.workspace?.project_dir ?? '/'

// Extract context window data for effective token calculation
const inputTokens = data.context_window?.current_usage?.input_tokens ?? 0
const outputTokens = data.context_window?.current_usage?.output_tokens ?? 0
const cacheCreationTokens = data.context_window?.current_usage?.cache_creation_input_tokens ?? 0
const cacheReadTokens = data.context_window?.current_usage?.cache_read_input_tokens ?? 0
const contextWindowSize = data.context_window?.context_window_size ?? 200000

const cost = data.cost?.total_cost_usd
const sessionId = data.session_id

// Calculate directory display
let dir: string
if (currentDir === projectDir) {
  // Scenario 2: current_dir equals project_dir - show only project name
  dir = projectDir.split('/').pop() ?? projectDir
}
else if (currentDir.startsWith(`${projectDir}/`)) {
  // Scenario 1: current_dir is within project_dir - show project name + relative path
  const projectName = projectDir.split('/').pop() ?? projectDir
  const relativePath = currentDir.slice(projectDir.length + 1)
  dir = `${projectName}/${relativePath}`
}
else {
  // Scenario 3: current_dir is outside project_dir - show project name + full current_dir in parentheses
  const projectName = projectDir.split('/').pop() ?? projectDir
  dir = `${projectName} (${currentDir})`
}

// Get git branch
let branch = ''
try {
  // Check if in a git repository
  execSync('git rev-parse --is-inside-work-tree', { stdio: 'ignore' })
  try {
    branch = execSync('git branch --show-current', { encoding: 'utf8' }).trim()
  }
  catch {
    branch = 'detached'
  }
}
catch {
  // Not in a git repository
}

// Build status line parts
const parts: Array<string> = []
parts.push(dir)
if (branch) parts.push(branch)
parts.push(model)

if (cost !== undefined) {
  const costDisplay = cost.toFixed(4)
  parts.push(`$${costDisplay}`)
}

// Calculate effective tokens and usage percentage
const effectiveTokens = inputTokens + outputTokens + cacheCreationTokens + cacheReadTokens
const usagePercentage = contextWindowSize > 0
  ? (effectiveTokens * 100 / contextWindowSize).toFixed(2)
  : '0.00'
parts.push(`${usagePercentage}%`)

// Load window state and calculate reset time
const stateFilePath = join(homedir(), '.claude.statusline-state.json')
const windowState = loadWindowState(stateFilePath)
const currentWindow = getCurrentWindow(windowState)

// Save updated state
writeFileSync(stateFilePath, JSON.stringify({ currentWindow }, null, 2))

// Add reset time
const resetTime = formatTimeUntilReset(currentWindow.endTime)
parts.push(resetTime)

// Add session_id at the end
if (sessionId !== undefined && sessionId !== '') {
  parts.push(sessionId)
}

// Join parts with " | "
const result = parts.join(' | ')

// Output with dim formatting (ANSI escape codes)
process.stdout.write(`\x1b[2m${result}\x1b[0m`)
