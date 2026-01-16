#!/usr/bin/env bun

// Git Guards: Enforce best practices for git commands
// - Multi-line Commit Message Guard: Deny commit messages with newlines
// - Git Add Without Commit Guard: Deny standalone git add (encourage chaining with &&)
// - Git Mutation Guard: Block git commands that mutate state (require human approval)

const input = JSON.parse(await Bun.stdin.text())
const command = (input.tool_input?.command ?? '').trim()

// Only applies to git commands
if (!/^git\s/.test(command)) {
  process.exit(0) // No opinion
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
    output('deny', 'Commit messages should be one line only. Please use a single-line commit message without newlines.')
    process.exit(0)
  }
}

// Git Add Without Commit Guard: Deny git add without git commit in the same command
if (/^git\s+add\b/.test(command) && !/git\s+commit\b/.test(command)) {
  output('deny',
    'Do not perform linked git operations separately. Commit messages should be single-line. Use `&&` to chain git add, commit, and push together so the user can approve everything all at once.\n\n' +
    'Example: git add foo.ts bar.ts && git commit -m "Your commit message" && git push')
  process.exit(0)
}

// Extract the git subcommand (second word)
const gitSubcommand = command.split(/\s+/)[1] ?? ''

// If it's NOT a read-only command, require approval
if (!GIT_READONLY_COMMANDS.includes(gitSubcommand)) {
  output('ask', 'Git Mutation Guard: This git command potentially modifies repository state and requires approval.')
  process.exit(0)
}

// Git command is read-only, no opinion
process.exit(0)
