import type { Rule } from './api';

type PolicyConditionRule = Pick<Rule, 'strategy' | 'limit_ms' | 'cooldown_every_ms' | 'credit_max_ms'>;

export function policyCondition(rule: PolicyConditionRule) {
  if (rule.strategy === 'cooldown') {
    return { label: 'Play duration', valueMs: rule.cooldown_every_ms };
  }
  if (rule.strategy === 'credit') {
    return { label: 'Maximum credit', valueMs: rule.credit_max_ms };
  }
  return { label: 'Limit', valueMs: rule.limit_ms };
}
