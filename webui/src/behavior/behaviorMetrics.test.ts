// webui/src/behavior/behaviorMetrics.test.ts
import { describe, expect, it } from 'vitest';
import {
  D_IDLE,
  T_ACTIVE_CAP_MS,
  T_GAP_MS,
  V_IDLE,
  V_TRAVEL,
} from './behaviorTypes';

describe('behavior thresholds', () => {
  it('exports design defaults', () => {
    expect(D_IDLE).toBe(500);
    expect(V_IDLE).toBe(50);
    expect(V_TRAVEL).toBe(800);
    expect(T_GAP_MS).toBe(5 * 60_000);
    expect(T_ACTIVE_CAP_MS).toBe(T_GAP_MS);
  });
});
