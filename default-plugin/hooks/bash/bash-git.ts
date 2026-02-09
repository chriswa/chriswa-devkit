#!/usr/bin/env bun

// Git Guards: Enforce best practices for git commands
// - Multi-line Commit Message Guard: Deny commit messages with newlines
// - Git Add Without Commit Guard: Deny standalone git add (encourage chaining with &&)
// - Git Mutation Guard: Block git commands that mutate state (require human approval)

import { appendFileSync } from "node:fs"
import { join } from "node:path"
import { homedir } from "node:os"

const input = JSON.parse(await Bun.stdin.text())
const command = (input.tool_input?.command ?? '').trim()

// Only applies to git commands
if (!/^git\s/.test(command)) {
  console.log(JSON.stringify({ hookSpecificOutput: { hookEventName: 'PreToolUse' }, continue: true, suppressOutput: true }))
  process.exit(0)
}

// Read-only git commands that are safe to run without approval
const GIT_READONLY_COMMANDS = [
  'status', 'diff', 'show', 'log', 'shortlog', 'reflog', 'blame', 'annotate',
  'grep', 'ls-files', 'ls-tree', 'ls-remote', 'cat-file', 'rev-parse',
  'rev-list', 'describe', 'name-rev', 'for-each-ref', 'var', 'fsck',
  'verify-commit', 'verify-tag', 'check-ignore', 'check-attr', 'check-mailmap',
  'diff-tree', 'diff-files', 'diff-index', 'range-diff', 'help', 'version',
  'count-objects', 'cherry', 'whatchanged', 'merge-base', 'get-tar-commit-id',
]

function output(decision: 'allow' | 'deny' | 'ask', reason: string): void {
  console.log(JSON.stringify({
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      permissionDecision: decision,
      permissionDecisionReason: reason,
    },
    systemMessage: reason,
  }))
}

// Multi-line Commit Message Guard: Deny commit messages with newlines
if (/git\s+commit\b/.test(command) && /-m\s/.test(command)) {
  if (/\n/.test(command)) {
    output('deny', 'Commit messages should be one line only. Add `&& git push` if pushing immediately after.')
    process.exit(0)
  }
}

// Git Add Without Commit Guard: Deny standalone git add (should be chained with &&)
if (/^git\s+add\b/.test(command) && !/&&/.test(command)) {
  output('deny',
    'Solitary git add is disallowed to avoid permission spam. Commit messages must be single-line or will be rejected. ' +
    'Chain git operations with `&&`. Include `&& git push` if pushing after.\n' +
    'Example: git add foo.ts && git commit -m "Message" && git push')
  process.exit(0)
}

// Extract the git subcommand, skipping known global flags like -C <path>
function extractGitSubcommand(cmd: string): string {
  const parts = cmd.split(/\s+/)
  let i = 1 // skip 'git'
  const flagsWithArg = new Set(['-C', '-c', '--git-dir', '--work-tree', '--namespace', '--super-prefix', '--exec-path'])
  const standaloneFlags = new Set(['--no-pager', '--bare', '--no-replace-objects', '--literal-pathspecs', '--glob-pathspecs', '--noglob-pathspecs', '--icase-pathspecs', '--no-optional-locks'])
  while (i < parts.length) {
    const part = parts[i]
    if (flagsWithArg.has(part)) {
      i += 2 // skip flag + its argument
    } else if ([...flagsWithArg].some(f => part.startsWith(f + '='))) {
      i += 1 // skip --flag=value form
    } else if (standaloneFlags.has(part)) {
      i += 1 // skip known standalone flag
    } else {
      return part
    }
  }
  return ''
}
const gitSubcommand = extractGitSubcommand(command)

// If it's NOT a read-only command, require approval
if (!GIT_READONLY_COMMANDS.includes(gitSubcommand)) {
  // Log mutation commands for later analysis
  const logPath = join(homedir(), '_claude_git_mutations.txt')
  appendFileSync(logPath, command + '\n\n')

  output('ask', 'Git Mutation Guard: This git command potentially modifies repository state and requires approval.')
  process.exit(0)
}

// Git command is read-only, no opinion
console.log(JSON.stringify({ hookSpecificOutput: { hookEventName: 'PreToolUse' }, continue: true, suppressOutput: true }))
process.exit(0)
