export type TrajectoryPointLike = {
  observed_at: string;
  segment_id: string;
  x: number;
  y: number;
  ping: number;
};

export const DEFAULT_TRAJECTORY_WINDOW_MS = 10 * 60_000;
export const DEFAULT_TRAJECTORY_MAX_POINTS = 12;

export type HybridOptions = {
  windowMs?: number;
  maxPoints?: number;
};

/** Latest sample at or before activeAt (same semantics as timeline focus). */
export function anchorSample<T extends TrajectoryPointLike>(samples: T[], activeAt: string | undefined): T | undefined {
  if (!samples.length) return undefined;
  const activeMS = activeAt ? Date.parse(activeAt) : Number.NaN;
  if (!Number.isFinite(activeMS)) return samples[0];
  return [...samples].reverse().find((s) => Date.parse(s.observed_at) <= activeMS) ?? samples[0];
}

export function hybridTrajectoryWindow<T extends TrajectoryPointLike>(
  samples: T[],
  activeAt: string | undefined,
  options: HybridOptions = {},
): T[] {
  const windowMs = options.windowMs ?? DEFAULT_TRAJECTORY_WINDOW_MS;
  const maxPoints = options.maxPoints ?? DEFAULT_TRAJECTORY_MAX_POINTS;
  const anchor = anchorSample(samples, activeAt);
  if (!anchor) return [];
  const activeMS = Date.parse(activeAt ?? anchor.observed_at);
  if (!Number.isFinite(activeMS)) return [anchor];

  const sameSegment = samples
    .filter((s) => s.segment_id === anchor.segment_id)
    .filter((s) => {
      const t = Date.parse(s.observed_at);
      return Number.isFinite(t) && Math.abs(t - activeMS) <= windowMs;
    })
    .sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at));

  if (sameSegment.length <= maxPoints) return sameSegment;

  const anchorIdx = sameSegment.findIndex((s) => s === anchor || s.observed_at === anchor.observed_at);
  const center = anchorIdx >= 0 ? anchorIdx : sameSegment.length - 1;
  // Prefer past on even/odd overflow: ceil((N-1)/2) before center, floor((N-1)/2) after
  let start = Math.max(0, center - Math.ceil((maxPoints - 1) / 2));
  let end = start + maxPoints;
  if (end > sameSegment.length) {
    end = sameSegment.length;
    start = Math.max(0, end - maxPoints);
  }
  return sameSegment.slice(start, end);
}

export type PingBin = 'lt50' | '50_80' | '80_120' | '120_200' | 'gt200' | 'unknown';

const PING_STYLES: Record<PingBin, { fill: string; stroke: string }> = {
  lt50: { fill: '#2f9e44', stroke: '#1b5e2a' },
  '50_80': { fill: '#82c91e', stroke: '#5c8a14' },
  '80_120': { fill: '#f59f00', stroke: '#b37100' },
  '120_200': { fill: '#f76707', stroke: '#b34a05' },
  gt200: { fill: '#e03131', stroke: '#9b2020' },
  unknown: { fill: '#868e96', stroke: '#495057' },
};

export function pingBin(ping: number): PingBin {
  if (!Number.isFinite(ping) || ping < 0) return 'unknown';
  if (ping < 50) return 'lt50';
  if (ping < 80) return '50_80';
  if (ping < 120) return '80_120';
  if (ping < 200) return '120_200';
  return 'gt200';
}

export function pingColor(ping: number): { bin: PingBin; fill: string; stroke: string } {
  const bin = pingBin(ping);
  return { bin, ...PING_STYLES[bin] };
}
