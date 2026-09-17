import { expect, it } from 'vitest';
import type { PlayerProgressResponse, ProgressCheckpoint } from '../api';
import { prepareRecentProgress, recentProgressText } from './recentProgress';

const cp = (id: number, value: number, extra: Partial<ProgressCheckpoint> = {}): ProgressCheckpoint => ({ id, observed_at: new Date(id * 1000).toISOString(), captured_at: new Date(id * 1000).toISOString(), world_id: 'w', source: 'save_import', schema_version: 1, consistent: true, boundary: '', metrics: { owned_pals: { state: 'known', value }, capture_total: { state: 'unknown' }, paldeck: { state: 'unknown' }, fast_travel: { state: 'unknown' } }, ...extra });
const data = (): PlayerProgressResponse => ({ user_id: 'u', status: 'available', baseline: null, checkpoints: [cp(1, 5), cp(2, 7)], changes: [{ id: 1, metric: 'owned_pals', before: 5, after: 7, delta: 2, added: [], removed: [], interval_start: new Date(1000).toISOString(), interval_end: new Date(2000).toISOString(), previous_checkpoint_id: 1, checkpoint_id: 2, rule_version: 1, confidence: 'observed', source: 'save_import' }], checkpoint_total: 2, change_total: 1 });

it('shows only a completed verified interval and labels its independent source/location', () => {
  const prepared = prepareRecentProgress(data(), 'u', 0, 5000);
  expect(recentProgressText(prepared, 1999)).toBeUndefined();
  expect(recentProgressText(prepared, 2000)).toContain('拥有帕鲁 5 → 7');
  expect(recentProgressText(prepared, 2000)).toContain('存档观测区间');
  expect(recentProgressText(prepared, 2000)).toContain('未关联此处地点');
  expect(recentProgressText(prepared, 1500)).toBeUndefined();
});

it('does not carry a prior change past reset, unknown world or inconsistent observations', () => {
  for (const extra of [{ boundary: 'counter_reset' }, { world_id: 'other' }, { consistent: false }, { schema_version: 2 }]) {
    const value = data(); value.checkpoints.push(cp(3, 7, extra)); value.checkpoint_total++;
    const prepared = prepareRecentProgress(value, 'u', 0, 5000);
    expect(recentProgressText(prepared, 2500)).toContain('5 → 7');
    expect(recentProgressText(prepared, 3000)).toBeUndefined();
  }
});

it('rejects mismatched players, incomplete endpoints, malformed saved changes and expired windows', () => {
  expect(recentProgressText(prepareRecentProgress(data(), 'other', 0, 5000), 3000)).toBeUndefined();
  const missing = data(); missing.checkpoints.shift();
  expect(recentProgressText(prepareRecentProgress(missing, 'u', 0, 5000), 3000)).toBeUndefined();
  const invalid = data(); invalid.changes[0].delta = 3;
  expect(recentProgressText(prepareRecentProgress(invalid, 'u', 0, 5000), 3000)).toBeUndefined();
  const prepared = prepareRecentProgress(data(), 'u', 0, 5000);
  expect(recentProgressText(prepared, 7000)).toBeUndefined();
  expect(recentProgressText(prepared, NaN)).toBeUndefined();
});

it('retains known evidence without claiming the total of a truncated response', () => {
  const partial = data(); partial.change_total = 20;
  const text = recentProgressText(prepareRecentProgress(partial, 'u', 0, 5000), 3000);
  expect(text).toContain('最近已知变化');
  expect(text).toContain('部分记录');
});

it('uses the same defensive regression boundary as journey summaries', () => {
  const value = data();
  for (const checkpoint of value.checkpoints) checkpoint.metrics.capture_total = { state: 'known', value: 10 };
  const reset = cp(3, 7); reset.metrics.capture_total = { state: 'known', value: 9 };
  value.checkpoints.push(reset); value.checkpoint_total++;
  const prepared = prepareRecentProgress(value, 'u', 0, 5000);
  expect(recentProgressText(prepared, 2500)).toContain('5 → 7');
  expect(recentProgressText(prepared, 3000)).toBeUndefined();
});
