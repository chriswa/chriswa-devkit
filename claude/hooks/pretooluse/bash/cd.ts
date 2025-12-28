import { Rule } from '../../../types'

// CD Guard: Block cd commands - they should be wrapped in a subshell
export const evaluate: Rule = ({ normalizedCommand }) => {
  // Only applies to cd commands
  if (!/^cd(\s|$)/.test(normalizedCommand)) {
    return null
  }

  // Block cd commands
  return {
    decision: 'deny',
    reason: 'CD Guard: cd commands should be wrapped in a subshell: (cd subdir && command)',
    priority: 30,
  }
}
