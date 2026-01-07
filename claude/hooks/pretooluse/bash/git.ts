import { Rule } from '../../../types'

// Git Guards: Enforce best practices for git commands
// - Git Add Without Commit Guard: Deny standalone git add (encourage chaining with &&)
// - Git Mutation Guard: Block git commands that mutate state (require human approval)
// Read-only git commands that are safe to run without approval
const GIT_READONLY_COMMANDS = [
  'status', 'diff', 'show', 'log', 'shortlog', 'reflog', 'blame', 'annotate',
  'grep', 'ls-files', 'ls-tree', 'ls-remote', 'cat-file', 'rev-parse',
  'rev-list', 'describe', 'name-rev', 'for-each-ref', 'var', 'fsck',
  'verify-commit', 'verify-tag', 'check-ignore', 'check-attr', 'check-mailmap',
  'diff-tree', 'diff-files', 'diff-index', 'range-diff', 'help', 'version',
  'count-objects', 'cherry', 'whatchanged', 'merge-base', 'get-tar-commit-id',
]

export const evaluate: Rule = ({ normalizedCommand }) => {
  // Only applies to git commands
  if (!/^git\s/.test(normalizedCommand)) {
    return null
  }

  // Multi-line Commit Message Guard: Deny commit messages with newlines
  // Check if this is a git commit command with a message
  if (/git\s+commit\b/.test(normalizedCommand) && /-m\s/.test(normalizedCommand)) {
    // Check if the commit message contains newlines
    // This regex looks for -m followed by a quoted string containing \n or actual newlines
    const hasNewlines = /\n/.test(normalizedCommand)

    if (hasNewlines) {
      return {
        decision: 'deny',
        reason: 'Commit messages should be one line only. Please use a single-line commit message without newlines.',
        priority: 120, // Highest priority to catch this early
      }
    }
  }

  // Git Add Without Commit Guard: Deny git add without git commit in the same command
  // This encourages using && to chain operations for atomic approval
  if (/^git\s+add\b/.test(normalizedCommand) && !/git\s+commit\b/.test(normalizedCommand)) {
    return {
      decision: 'deny',
      reason:
        'Do not perform linked git operations separately. Commit messages should be single-line. Use `&&` to chain git add, commit, and push together so the user can approve everything all at once.\n\n' +
        'Example: git add foo.ts bar.ts && git commit -m "Your commit message" && git push',
      priority: 110, // Higher priority than general git mutation guard
    }
  }

  // Extract the git subcommand (second word)
  const gitSubcommand = normalizedCommand.split(/\s+/)[1] ?? ''

  // Check if it's NOT a read-only command
  if (!GIT_READONLY_COMMANDS.includes(gitSubcommand)) {
    return {
      decision: 'ask',
      reason: 'Git Mutation Guard: This git command potentially modifies repository state and requires approval.',
      priority: 100,
    }
  }

  // Git command is read-only, no opinion
  return null
}
