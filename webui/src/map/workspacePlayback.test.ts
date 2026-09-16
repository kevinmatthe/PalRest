import { describe, expect, it } from 'vitest';
import type { TrajectorySample } from '../api';
import { connectionBreak, playbackFrame, prepareTrajectory, trajectoryRuns } from './workspacePlayback';

const start = Date.parse('2026-09-16T10:00:00Z');
const point = (seconds: number, x: number, extra: Partial<TrajectorySample> = {}): TrajectorySample => ({
  user_id: 'u1', segment_id: 's1', runtime_epoch: 1, observed_at: new Date(start + seconds * 1000).toISOString(),
  x, y: 10000, ping: 30, level: 10, source_ref: `p${seconds}`, ...extra,
});

describe('continuous map playback', () => {
  it('interpolates coordinates without interpolating observed attributes or changing evidence', () => {
    const samples = [point(0, 1000), point(30, 4000, { level: 11 })];
    const frame = playbackFrame(prepareTrajectory(samples), start + 15000);
    expect(frame).toMatchObject({ x: 2500, y: 10000, interpolated: true, sample: { level: 10 } });
    expect(samples[0].x).toBe(1000);
  });
  it('does not show future positions before the first observation or extrapolate after the last', () => {
    const samples = prepareTrajectory([point(0, 1000), point(30, 4000)]);
    expect(playbackFrame(samples, start - 1)).toBeNull();
    expect(playbackFrame(samples, start + 60000)).toMatchObject({ x: 4000, interpolated: false, status: 'last-known' });
    expect(playbackFrame(samples, start + 30000)).toMatchObject({ x: 4000, status: 'observed' });
  });
  it.each([
    [{ segment_id: 's2' }, 'segment'],
    [{ runtime_epoch: 2 }, 'restart'],
    [{ user_id: 'u2' }, 'player'],
    [{ x: 100000 }, 'teleport'],
  ] as const)('never connects incompatible observations %j', (extra, reason) => {
    const samples = prepareTrajectory([point(0, 1000), point(30, 4000, extra)]);
    expect(connectionBreak(samples[0], samples[1])).toBe(reason);
    expect(playbackFrame(samples, start + 15000)).toMatchObject({ x: 1000, interpolated: false, status: 'gap', nextAt: start + 30000 });
    expect(trajectoryRuns(samples)).toHaveLength(2);
  });
  it('keeps long gaps unknown and exposes where to resume', () => {
    const samples = prepareTrajectory([point(0, 1000), point(301, 2000)]);
    expect(playbackFrame(samples, start + 60000)).toMatchObject({ status: 'gap', reason: 'gap', nextAt: start + 301000 });
  });
  it('sorts valid evidence and rejects invalid values and ambiguous timestamps', () => {
    const samples = prepareTrajectory([point(30, 3000), point(0, 1000), point(20, NaN), point(25, 5, { observed_at: 'invalid' })]);
    expect(samples.map(p => p.x)).toEqual([1000, 3000]);
    expect(connectionBreak(point(0, 1), point(0, 2))).toBe('time');
  });
  it('preserves breaks across invalid evidence rather than bridging over it', () => {
    const samples = prepareTrajectory([point(0, 1000), point(10, NaN), point(20, 2000)]);
    expect(playbackFrame(samples, start + 5000)?.status).toBe('gap');
  });
});
