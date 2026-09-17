import type { TrajectorySample } from '../api';
import { TELEPORT_MIN_DIST, T_GAP_MS } from '../behavior/behaviorTypes';

export type PreparedSample = TrajectorySample & { time: number; breakBefore?: boolean };
export type BreakReason = 'player' | 'restart' | 'segment' | 'time' | 'gap' | 'teleport' | 'invalid' | 'continuity';
export type PlaybackFrame = {
  x: number; y: number; sample: PreparedSample; index: number; interpolated: boolean;
  status: 'observed' | 'interpolated' | 'gap' | 'last-known'; reason?: BreakReason; nextAt?: number;
};

/** Invalid observations are barriers, never shortcuts across missing evidence. */
export function prepareTrajectory(input: TrajectorySample[]): PreparedSample[] {
  const invalidTime = input.some(p => !Number.isFinite(Date.parse(p.observed_at)));
  const sorted = input.map(p => ({ ...p, time: Date.parse(p.observed_at) }))
    .filter(p => Number.isFinite(p.time)).sort((a, b) => a.time - b.time);
  const result: PreparedSample[] = [];
  let barrier = false;
  for (let i = 0; i < sorted.length; i++) {
    const p = sorted[i];
    if (!Number.isFinite(p.x) || !Number.isFinite(p.y)) { barrier = true; continue; }
    const duplicate = sorted[i - 1]?.time === p.time || sorted[i + 1]?.time === p.time;
    result.push({ ...p, breakBefore: barrier || invalidTime || duplicate });
    barrier = duplicate;
  }
  return result;
}

export function hasTrajectoryContinuity(sample: TrajectorySample): boolean {
  return Boolean(sample.segment_id) && Number.isSafeInteger(sample.runtime_epoch) && sample.runtime_epoch >= 0;
}

export function connectionBreak(a: TrajectorySample, b: TrajectorySample & { breakBefore?: boolean }): BreakReason | undefined {
  if (a.user_id !== b.user_id) return 'player';
  if (!hasTrajectoryContinuity(a) || !hasTrajectoryContinuity(b)) return 'continuity';
  if (a.runtime_epoch !== b.runtime_epoch) return 'restart';
  if (!a.segment_id || a.segment_id !== b.segment_id) return 'segment';
  const dt = Date.parse(b.observed_at) - Date.parse(a.observed_at);
  if (!Number.isFinite(dt) || dt <= 0) return 'time';
  if (b.breakBefore || ![a.x, a.y, b.x, b.y].every(Number.isFinite)) return 'invalid';
  if (dt > T_GAP_MS) return 'gap';
  if (Math.hypot(b.x - a.x, b.y - a.y) >= TELEPORT_MIN_DIST) return 'teleport';
  return undefined;
}

/** Display coordinates only: observed attributes always come from the prior checkpoint. */
export function playbackFrame(samples: PreparedSample[], time: number): PlaybackFrame | null {
  if (!samples.length || !Number.isFinite(time) || time < samples[0].time) return null;
  let low = 0;
  let high = samples.length;
  while (low < high) {
    const mid = (low + high) >>> 1;
    if (samples[mid].time <= time) low = mid + 1;
    else high = mid;
  }
  const index = low - 1;
  const sample = samples[index];
  const next = samples[index + 1];
  const base: PlaybackFrame = { x: sample.x, y: sample.y, sample, index, interpolated: false, status: 'observed', nextAt: next?.time };
  if (time === sample.time) return base;
  if (!next) return { ...base, status: 'last-known' };
  const reason = connectionBreak(sample, next);
  if (reason) return { ...base, status: 'gap', reason };
  const ratio = (time - sample.time) / (next.time - sample.time);
  return { ...base, x: sample.x + (next.x - sample.x) * ratio, y: sample.y + (next.y - sample.y) * ratio, interpolated: true, status: 'interpolated' };
}

export function trajectoryRuns(samples: PreparedSample[]): PreparedSample[][] {
  const runs: PreparedSample[][] = [];
  samples.forEach((p, index) => {
    if (index === 0 || connectionBreak(samples[index - 1], p)) runs.push([]);
    runs[runs.length - 1].push(p);
  });
  return runs;
}

export const BREAK_LABELS: Record<BreakReason, string> = {
  player: '玩家边界', restart: '服务器重启', segment: '轨迹中断', time: '时间不连续',
  continuity: '连续性信息不足', gap: '观测缺口', teleport: '疑似传送', invalid: '无效观测',
};
