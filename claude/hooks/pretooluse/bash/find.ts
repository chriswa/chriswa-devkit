import { Rule } from '../../../types'

// Find Guard: Block find commands that don't exclude node_modules (causes excessive delays)
export const evaluate: Rule = ({ normalizedCommand }) => {
  // Only applies to find commands
  if (!/^find\s/.test(normalizedCommand)) {
    return null
  }

  // If node_modules is excluded, allow it
  if (normalizedCommand.includes('node_modules')) {
    return null
  }

  // Find command without node_modules exclusion
  return {
    decision: 'deny',
    reason: 'To avoid excessive delays, do not use `find` without excluding `node_modules`',
    priority: 50,
  }
}
