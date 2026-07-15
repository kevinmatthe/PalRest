import { describe, expect, it } from 'vitest';

import { policyCondition } from './policyCondition';

describe('policyCondition', () => {
  it.each([
    ['fixed_window', '限额', 8 * 60 * 60 * 1000],
    ['cooldown', '游玩时长', 2 * 60 * 60 * 1000],
    ['credit', '额度上限', 3 * 60 * 60 * 1000],
  ])('maps %s to its strategy-specific condition', (strategy, label, valueMs) => {
    expect(policyCondition({
      strategy,
      limit_ms: 8 * 60 * 60 * 1000,
      cooldown_every_ms: 2 * 60 * 60 * 1000,
      credit_max_ms: 3 * 60 * 60 * 1000,
    })).toEqual({ label, valueMs });
  });

  it('uses maximum credit instead of a stale fixed-window limit for credit rules', () => {
    expect(policyCondition({
      strategy: 'credit',
      limit_ms: 8 * 60 * 60 * 1000,
      credit_max_ms: 3 * 60 * 60 * 1000,
    })).toEqual({ label: '额度上限', valueMs: 3 * 60 * 60 * 1000 });
  });
});
