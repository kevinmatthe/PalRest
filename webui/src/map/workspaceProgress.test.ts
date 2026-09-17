import { expect, it } from 'vitest';
import { progressAt, progressMetricValue } from './workspaceProgress';
import type { PlayerProgressResponse, ProgressCheckpoint } from '../api';
const checkpoint = (id: number, time: number): ProgressCheckpoint => ({ id, observed_at: new Date(time).toISOString(), captured_at: new Date(time).toISOString(), world_id: 'w', source: 'save_import', schema_version: 1, consistent: true, boundary: '', metrics: { owned_pals: { state: 'known', value: id }, capture_total: { state: 'unknown' }, paldeck: { state: 'unsupported' }, fast_travel: { state: 'unknown' } } });
const data: PlayerProgressResponse = { user_id: 'u', status: 'available', baseline: checkpoint(1, 1000), checkpoints: [checkpoint(2, 2000), checkpoint(3, 3000)], changes: [{ id: 1, metric: 'owned_pals', before: 1, after: 2, delta: 1, added: [], removed: [], interval_start: new Date(1000).toISOString(), interval_end: new Date(2000).toISOString(), previous_checkpoint_id: 1, checkpoint_id: 2, rule_version: 1, confidence: 'observed', source: 'save_import' }], checkpoint_total: 2, change_total: 1 };
it('never substitutes zero for unknown, unsupported or missing counts', () => {
  expect(progressMetricValue(undefined)).toBe('未采集');
  expect(progressMetricValue({ state: 'known' })).toBe('未采集');
  expect(progressMetricValue({ state: 'unknown', value: 0 })).toBe('未采集');
  expect(progressMetricValue({ state: 'unsupported' })).toBe('暂不支持');
  expect(progressMetricValue({ state: 'known', value: 0 })).toBe('0');
});
it('uses baseline before first observation and hides all future checkpoints and changes', () => {
  expect(progressAt(data, 1500).checkpoint?.id).toBe(1);
  expect(progressAt(data, 1500).changes).toHaveLength(0);
  expect(progressAt(data, 500).checkpoint).toBeNull();
  expect(progressAt(data, 2500).checkpoint?.id).toBe(2);
  expect(progressAt(data, 2500).changes).toHaveLength(1);
});
it('does not carry an older known value over a missing observation', () => {
  const next = checkpoint(2, 2000); next.metrics.owned_pals = { state: 'unknown' };
  expect(progressMetricValue(progressAt({ ...data, checkpoints: [next] }, 2500).checkpoint?.metrics.owned_pals)).toBe('未采集');
});
