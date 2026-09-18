import { afterEach, expect, it, vi } from 'vitest';
import { getPlayerProgress, getPlayerTimeline, type PlayerProgressResponse, type PlayerTimelineResponse, type ProgressCheckpoint, type ProgressChange } from '../api';
import { loadProgressWindow, loadTimelineWindow } from './completeWindow';
vi.mock('../api', () => ({ getPlayerProgress: vi.fn(), getPlayerTimeline: vi.fn() }));
afterEach(() => vi.resetAllMocks());
const timestamp = (t: number) => new Date(t).toISOString();
const checkpoint = (id: number, t: number): ProgressCheckpoint => ({ id, observed_at: timestamp(t), captured_at: timestamp(t), world_id: 'w', schema_version: 1, source: 'save_import', consistent: true, boundary: '', metrics: { fast_travel: { state: 'unknown' }, owned_pals: { state: 'unknown' }, capture_total: { state: 'unknown' }, paldeck: { state: 'unknown' } } });
const progress = (checkpoints: ProgressCheckpoint[], total = checkpoints.length): PlayerProgressResponse => ({ user_id: 'u', status: 'available', baseline: null, checkpoints, changes: [], checkpoint_total: total, change_total: 0 });
const timeline = (): PlayerTimelineResponse => ({ user_id: 'u', trajectories: [], events: [], private_samples: [], trajectory_total: 0, event_total: 0 });

it('loads both halves of a truncated progress window and retains its original baseline', async () => {
  const a = checkpoint(1, 1), b = checkpoint(2, 5), baseline = checkpoint(0, -1);
  vi.mocked(getPlayerProgress).mockResolvedValueOnce({ ...progress([b], 2), baseline })
    .mockResolvedValueOnce({ ...progress([a]), baseline }).mockResolvedValueOnce({ ...progress([b]), baseline: a });
  const data = await loadProgressWindow('u', 0, 10, new AbortController().signal);
  expect(data.checkpoints.map(c => c.id)).toEqual([1, 2]);
  expect(data.baseline?.id).toBe(0);
  expect(data.checkpoint_total).toBe(2);
  expect(vi.mocked(getPlayerProgress).mock.calls.map(c => c.slice(1, 3))).toEqual([
    [timestamp(0), timestamp(10)], [timestamp(0), timestamp(5)], [timestamp(5), timestamp(10)],
  ]);
});
it('splits when changes alone are truncated, including several metrics at the same timestamp', async () => {
  const a = checkpoint(1, 1), b = checkpoint(2, 5);
  const change: ProgressChange = { added: [], removed: [], id: 1, metric: 'level' as const, before: 1, after: 2, delta: 1, interval_start: a.observed_at, interval_end: b.observed_at, previous_checkpoint_id: 1, checkpoint_id: 2, rule_version: 1, confidence: 'observed' as const, source: 'save_import' as const };
  vi.mocked(getPlayerProgress).mockResolvedValueOnce({ ...progress([a, b]), changes: [change], change_total: 2 })
    .mockResolvedValueOnce(progress([a])).mockResolvedValueOnce({ ...progress([b]), baseline: a, changes: [change, { ...change, id: 2, metric: 'experience' }], change_total: 2 });
  const data = await loadProgressWindow('u', 0, 10, new AbortController().signal);
  expect(data.changes.map(c => c.id).sort()).toEqual([1, 2]);
  expect(data.change_total).toBe(2);
});
it('loads all timeline pages without rounding nanosecond timestamps into pagination gaps', async () => {
  const sample = (t: string) => ({ user_id: 'u', observed_at: t, x: 0, y: 0, level: 1, ping: 0, segment_id: 's', runtime_epoch: 1, source_ref: t });
  const a = sample('1970-01-01T00:00:00.004999999Z'), b = sample('1970-01-01T00:00:00.005000001Z');
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce({ ...timeline(), trajectories: [b], trajectory_total: 2 })
    .mockResolvedValueOnce({ ...timeline(), trajectories: [a], trajectory_total: 1 })
    .mockResolvedValueOnce({ ...timeline(), trajectories: [b], trajectory_total: 1 });
  const data = await loadTimelineWindow('u', 0, 10, new AbortController().signal);
  expect(data.trajectories).toEqual([a, b]);
  expect(data.trajectory_total).toBe(2);
});
it('rejects saturated timestamps rather than passing a truncated window as complete', async () => {
  vi.mocked(getPlayerProgress).mockResolvedValue(progress([checkpoint(1, 0)], 501));
  await expect(loadProgressWindow('u', 0, 1, new AbortController().signal)).rejects.toThrow('完整');
});
it('aborts between pages and rejects mixed player responses', async () => {
  const controller = new AbortController();
  vi.mocked(getPlayerTimeline).mockImplementationOnce(async () => { controller.abort(); return timeline(); });
  await expect(loadTimelineWindow('u', 0, 10, controller.signal)).rejects.toThrow();
  vi.mocked(getPlayerProgress).mockResolvedValue({ ...progress([]), user_id: 'other' });
  await expect(loadProgressWindow('u', 0, 10, new AbortController().signal)).rejects.toThrow('玩家');
});
