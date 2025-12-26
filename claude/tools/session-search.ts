#!/usr/bin/env bun

import { existsSync, readdirSync, readFileSync, statSync } from 'fs'
import { homedir } from 'os'
import { basename, join } from 'path'
import { z } from 'zod'

const homeDir = homedir()
const claudeProjectsDir = join(homeDir, '.claude', 'projects')

// ANSI color codes (purpose-based names)
const colors = {
  reset: '\x1b[0m',
  dim: '\x1b[2m',
  matchBg: '\x1b[48;5;27m', // Background for search term match
  matchFg: '\x1b[97m', // Foreground for search term match
  headerBg: '\x1b[48;5;23m', // Background for session header (dark cyan)
  headerFg: '\x1b[97m', // Foreground for session header
  newlineFg: '\x1b[97;1m', // Foreground for \n markers
}

// Zod schemas
const ContentBlockSchema = z.object({
  type: z.string(),
  text: z.string().optional(),
})

const ParsedLineSchema = z.object({
  type: z.string().optional(),
  message: z.object({
    content: z.union([z.string(), z.array(ContentBlockSchema)]).optional(),
  }).optional(),
  cwd: z.string().optional(),
  timestamp: z.string().optional(),
  summary: z.string().optional(),
})

type ParsedLine = z.infer<typeof ParsedLineSchema>

interface SessionMetadata {
  cwd: string | null
  summary: string | null
  createdAt: string | null
  lastModifiedAt: string | null
  humanMessageCount: number
}

interface ContextLine {
  lineNumber: number
  type: string
  content: string
}

interface SearchResult {
  sessionFile: string
  sessionId: string
  metadata: SessionMetadata
  lineNumber: number
  type: string
  matchContext: string
  contextBefore: Array<ContextLine>
  contextAfter: Array<ContextLine>
}

interface Options {
  jsonOutput: boolean
  sessionsOnly: boolean
  userOnly: boolean
  assistantOnly: boolean
  days: number | null
  searchString: string | null
}

function parseLine(line: string): ParsedLine | null {
  try {
    const result = ParsedLineSchema.safeParse(JSON.parse(line))
    return result.success ? result.data : null
  }
  catch {
    return null
  }
}

function getLineType(line: string): string {
  const parsed = parseLine(line)
  const type = parsed?.type ?? 'parse-error'
  // Rename assistant to agent for display
  return type === 'assistant' ? 'agent' : type
}

function truncateLine(line: string, maxLen = 120): string {
  // Replace newlines with visible \n marker
  const escaped = line.replace(/\n/g, `${colors.newlineFg}\\n${colors.reset}${colors.dim}`)
  if (escaped.length <= maxLen) return escaped
  return `${escaped.substring(0, maxLen)}...`
}

function highlightMatch(text: string, searchString: string): string {
  const lowerText = text.toLowerCase()
  const lowerSearch = searchString.toLowerCase()
  const index = lowerText.indexOf(lowerSearch)

  if (index === -1) return text

  const before = text.substring(0, index)
  const match = text.substring(index, index + searchString.length)
  const after = text.substring(index + searchString.length)

  // Highlight the match
  return `${before}${colors.matchBg}${colors.matchFg}${match}${colors.reset}${after}`
}

function extractContent(line: string): string | null {
  const parsed = parseLine(line)
  if (parsed?.message?.content !== undefined) {
    const content = parsed.message.content
    if (typeof content === 'string') {
      return content
    }
    else if (Array.isArray(content)) {
      // For assistant messages, content is an array of content blocks
      // Extract text from text blocks, skip tool_use and tool_result blocks
      const textParts = content
        .filter((block) => block.type === 'text' && block.text)
        .map((block) => block.text!)
      if (textParts.length > 0) {
        return textParts.join('\n')
      }
      // Skip if only tool_use/tool_result blocks
    }
  }
  return null
}

function extractSessionMetadata(lines: Array<string>): SessionMetadata {
  let cwd: string | null = null
  let summary: string | null = null
  let createdAt: string | null = null
  let lastModifiedAt: string | null = null
  let humanMessageCount = 0

  for (const line of lines) {
    const parsed = parseLine(line)
    if (!parsed) continue

    // Get summary from first line
    if (parsed.type === 'summary' && parsed.summary) {
      summary = parsed.summary
    }

    // Get cwd from first message that has it
    if (!cwd && parsed.cwd) {
      cwd = parsed.cwd
    }

    // Get timestamps from user/assistant messages
    if ((parsed.type === 'user' || parsed.type === 'assistant') && parsed.timestamp) {
      createdAt ??= parsed.timestamp
      lastModifiedAt = parsed.timestamp
    }

    // Count human text messages (not tool results)
    if (parsed.type === 'user' && parsed.message?.content) {
      if (typeof parsed.message.content === 'string') {
        humanMessageCount++
      }
    }
  }

  return { cwd, summary, createdAt, lastModifiedAt, humanMessageCount }
}

function searchSessionFile(
  filePath: string,
  searchString: string,
  contextLines = 2,
  typeFilter: Array<string> | null = null,
): Array<SearchResult> {
  const results: Array<SearchResult> = []
  const sessionId = basename(filePath, '.jsonl')

  // Read all lines into memory for context
  const content = readFileSync(filePath, 'utf8')
  const lines = content.split('\n').filter((l) => l.trim())

  // Extract session metadata
  const metadata = extractSessionMetadata(lines)

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const lineNumber = i + 1
    const parsed = parseLine(line)
    const type = parsed?.type ?? 'parse-error'

    // Apply type filter if specified
    if (typeFilter && !typeFilter.includes(type)) {
      continue
    }

    // Extract the actual message content to search (same logic as extractContent)
    const contentToSearch = extractContent(lines[i]) ?? ''

    if (contentToSearch.toLowerCase().includes(searchString.toLowerCase())) {
      // Gather context lines
      const contextBefore: Array<ContextLine> = []
      const contextAfter: Array<ContextLine> = []

      // Search backwards for context lines with actual content (skip tool results)
      for (let j = i - 1; j >= 0 && contextBefore.length < contextLines; j--) {
        const ctxContent = extractContent(lines[j])
        if (ctxContent) {
          contextBefore.unshift({
            lineNumber: j + 1,
            type: getLineType(lines[j]),
            content: truncateLine(ctxContent, 150),
          })
        }
      }

      // Search forwards for context lines with actual content (skip tool results)
      for (let j = i + 1; j < lines.length && contextAfter.length < contextLines; j++) {
        const ctxContent = extractContent(lines[j])
        if (ctxContent) {
          contextAfter.push({
            lineNumber: j + 1,
            type: getLineType(lines[j]),
            content: truncateLine(ctxContent, 150),
          })
        }
      }

      // Find the match context within the content (show more context)
      const lowerContent = contentToSearch.toLowerCase()
      const matchIndex = lowerContent.indexOf(searchString.toLowerCase())
      const contextStart = Math.max(0, matchIndex - 100)
      const contextEnd = Math.min(contentToSearch.length, matchIndex + searchString.length + 200)
      let matchContext = contentToSearch.substring(contextStart, contextEnd)
      // Replace newlines with visible \n marker
      matchContext = matchContext.replace(/\n/g, `${colors.newlineFg}\\n${colors.reset}`)
      if (contextStart > 0) matchContext = `...${matchContext}`
      if (contextEnd < contentToSearch.length) matchContext = `${matchContext}...`

      results.push({
        sessionFile: filePath,
        sessionId,
        metadata,
        lineNumber,
        type,
        matchContext,
        contextBefore,
        contextAfter,
      })
    }
  }

  return results
}

function findAllSessionFiles(maxAgeDays: number | null = null): Array<string> {
  const sessionFiles: Array<string> = []

  if (!existsSync(claudeProjectsDir)) {
    process.stderr.write(`Error: Claude projects directory not found: ${claudeProjectsDir}\n`)
    process.exit(1)
  }

  const cutoffTime = maxAgeDays !== null ? Date.now() - maxAgeDays * 24 * 60 * 60 * 1000 : null

  // Recursively find all .jsonl files in the projects directory
  function walkDir(dir: string): void {
    const entries = readdirSync(dir, { withFileTypes: true })
    for (const entry of entries) {
      const fullPath = join(dir, entry.name)
      if (entry.isDirectory()) {
        walkDir(fullPath)
      }
      else if (entry.isFile() && entry.name.endsWith('.jsonl')) {
        // Skip subagent sessions (agent-*)
        const sessionId = basename(entry.name, '.jsonl')
        if (sessionId.startsWith('agent-')) {
          continue
        }

        // Filter by modification time if maxAgeDays is set
        if (cutoffTime !== null) {
          const stat = statSync(fullPath)
          if (stat.mtimeMs < cutoffTime) {
            continue
          }
        }
        sessionFiles.push(fullPath)
      }
    }
  }

  walkDir(claudeProjectsDir)
  return sessionFiles
}

function parseArgs(args: Array<string>): Options {
  const result: Options = {
    jsonOutput: false,
    sessionsOnly: false,
    userOnly: false,
    assistantOnly: false,
    days: null,
    searchString: null,
  }

  for (let i = 0; i < args.length; i++) {
    const arg = args[i]
    if (arg === '--json') {
      result.jsonOutput = true
    }
    else if (arg === '--sessions-only') {
      result.sessionsOnly = true
    }
    else if (arg === '--user') {
      result.userOnly = true
    }
    else if (arg === '--assistant') {
      result.assistantOnly = true
    }
    else if (arg === '--days' && i + 1 < args.length) {
      result.days = Number.parseInt(args[i + 1], 10)
      i++ // skip next arg
    }
    else if (!arg.startsWith('--')) {
      result.searchString = arg
    }
  }

  return result
}

function main(): void {
  const args = process.argv.slice(2)

  if (args.length === 0) {
    process.stdout.write('Usage: claude-session-search <search-string> [options]\n')
    process.stdout.write('\n')
    process.stdout.write('Options:\n')
    process.stdout.write('  --json          Output results as JSON\n')
    process.stdout.write('  --sessions-only Only show session IDs with matches\n')
    process.stdout.write('  --user          Only search in user messages\n')
    process.stdout.write('  --assistant     Only search in assistant messages\n')
    process.stdout.write('  --days <n>      Only search sessions modified in last n days\n')
    process.stdout.write('\n')
    process.stdout.write('Searches all Claude Code session files in ~/.claude/projects/\n')
    process.exit(1)
  }

  const opts = parseArgs(args)

  if (!opts.searchString) {
    process.stderr.write('Error: No search string provided\n')
    process.exit(1)
  }

  // Build type filter (use internal type names)
  let typeFilter: Array<string> | null = null
  if (opts.userOnly && !opts.assistantOnly) {
    typeFilter = ['user']
  }
  else if (opts.assistantOnly && !opts.userOnly) {
    typeFilter = ['agent'] // maps to 'assistant' internally after getLineType
  }
  else if (opts.userOnly && opts.assistantOnly) {
    typeFilter = ['user', 'agent']
  }

  process.stderr.write(`Searching for: "${opts.searchString}"\n`)
  if (typeFilter) {
    process.stderr.write(`Filtering by type: ${typeFilter.join(', ')}\n`)
  }
  if (opts.days !== null) {
    process.stderr.write(`Limiting to sessions modified in last ${opts.days} days\n`)
  }

  const sessionFiles = findAllSessionFiles(opts.days)
  process.stderr.write(`Found ${sessionFiles.length} session files\n`)

  const allResults: Array<SearchResult> = []
  const sessionsWithMatches = new Set<string>()

  for (const file of sessionFiles) {
    const results = searchSessionFile(file, opts.searchString, 2, typeFilter)
    if (results.length > 0) {
      sessionsWithMatches.add(results[0].sessionId)
      allResults.push(...results)
    }
  }

  if (opts.sessionsOnly) {
    if (opts.jsonOutput) {
      process.stdout.write(`${JSON.stringify([...sessionsWithMatches], null, 2)}\n`)
    }
    else {
      for (const sessionId of sessionsWithMatches) {
        process.stdout.write(`${sessionId}\n`)
      }
    }
  }
  else if (opts.jsonOutput) {
    process.stdout.write(`${JSON.stringify(allResults, null, 2)}\n`)
  }
  else {
    process.stderr.write(`\nFound ${allResults.length} matches in ${sessionsWithMatches.size} sessions:\n\n`)

    // Group results by session
    const resultsBySession = new Map<
      string,
      { metadata: SessionMetadata, results: Array<SearchResult> }
    >()
    for (const result of allResults) {
      if (!resultsBySession.has(result.sessionId)) {
        resultsBySession.set(result.sessionId, {
          metadata: result.metadata,
          results: [],
        })
      }
      resultsBySession.get(result.sessionId)!.results.push(result)
    }

    // Sort sessions by created date (oldest first)
    const sortedSessions = [...resultsBySession.entries()].sort((a, b) => {
      const dateA = a[1].metadata.createdAt ?? ''
      const dateB = b[1].metadata.createdAt ?? ''
      return dateA.localeCompare(dateB)
    })

    // Display grouped by session
    for (const [sessionId, session] of sortedSessions) {
      const { metadata } = session
      const cwd = metadata.cwd ?? '~'
      const command = `(cd ${cwd} && claude --resume ${sessionId})`
      process.stdout.write(`${colors.headerBg}${colors.headerFg} ${command} ${colors.reset}\n`)

      // Display metadata
      const created = metadata.createdAt ? new Date(metadata.createdAt).toLocaleString() : 'unknown'
      const modified = metadata.lastModifiedAt
        ? new Date(metadata.lastModifiedAt).toLocaleString()
        : 'unknown'
      const summary = metadata.summary ?? '(no summary)'
      process.stdout.write(`Created: ${created} | Modified: ${modified} | Messages: ${metadata.humanMessageCount}\n`)
      process.stdout.write(`Summary: ${summary}\n`)
      process.stdout.write('\n')

      for (const result of session.results) {
        process.stdout.write(`  Line ${result.lineNumber} (${result.type}):\n`)

        // Show context before (dimmed)
        for (const ctx of result.contextBefore) {
          process.stdout.write(`${colors.dim}    ${ctx.lineNumber} (${ctx.type}): ${ctx.content}${colors.reset}\n`)
        }

        // Show the matching line (match highlighted in blue)
        const highlightedContent = highlightMatch(result.matchContext, opts.searchString)
        process.stdout.write(`  ► ${result.lineNumber} (${result.type}): ${highlightedContent}\n`)

        // Show context after (dimmed)
        for (const ctx of result.contextAfter) {
          process.stdout.write(`${colors.dim}    ${ctx.lineNumber} (${ctx.type}): ${ctx.content}${colors.reset}\n`)
        }

        process.stdout.write('\n')
      }
    }
  }
}

main()
