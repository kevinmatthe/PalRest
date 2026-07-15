import type { Rule } from './api';

type PolicyConditionRule = Pick<Rule, 'strategy' | 'limit_ms' | 'cooldown_every_ms' | 'credit_max_ms'>;

export function policyCondition(rule: PolicyConditionRule) {
  if (rule.strategy === 'cooldown') {
    return { label: '游玩时长', valueMs: rule.cooldown_every_ms };
  }
  if (rule.strategy === 'credit') {
    return { label: '额度上限', valueMs: rule.credit_max_ms };
  }
  return { label: '限额', valueMs: rule.limit_ms };
}
