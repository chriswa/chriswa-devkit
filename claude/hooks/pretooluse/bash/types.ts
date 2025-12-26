// Shared types for pretooluse-bash rule system

export interface RuleContext {
  toolName: string
  command: string
  normalizedCommand: string
}

export interface RuleDecision {
  decision: 'allow' | 'deny' | 'ask'
  reason: string
  priority: number
}

export type RuleResult = null | RuleDecision

export type Rule = (context: RuleContext) => RuleResult
