export type TrajectoryPointLike = {
  observed_at: string;
  segment_id: string;
  x: number;
  y: number;
  ping: number;
};

export const DEFAULT_TRAJECTORY_WINDOW_MS = 10 * 60_000;
export const DEFAULT_TRAJECTORY_MAX_POINTS = 16;

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

export type TrajectorySplit<T> = {
  past: T[];
  future: T[];
  anchor: T | undefined;
  anchorIndex: number;
};

export function splitTrajectoryPastFuture<T extends TrajectoryPointLike>(
  windowSamples: T[],
  activeAt: string | undefined,
): TrajectorySplit<T> {
  if (!windowSamples.length) {
    return { past: [], future: [], anchor: undefined, anchorIndex: -1 };
  }
  const anchor = anchorSample(windowSamples, activeAt);
  let anchorIndex = anchor
    ? windowSamples.findIndex((s) => s === anchor || s.observed_at === anchor.observed_at)
    : -1;
  if (anchorIndex < 0) {
    const activeMS = activeAt ? Date.parse(activeAt) : Number.NaN;
    if (Number.isFinite(activeMS)) {
      for (let i = windowSamples.length - 1; i >= 0; i -= 1) {
        const t = Date.parse(windowSamples[i]!.observed_at);
        if (Number.isFinite(t) && t <= activeMS) {
          anchorIndex = i;
          break;
        }
      }
    }
    if (anchorIndex < 0) anchorIndex = 0;
  }
  const resolved = windowSamples[anchorIndex]!;
  return {
    past: windowSamples.slice(0, anchorIndex + 1),
    future: windowSamples.slice(anchorIndex),
    anchor: resolved,
    anchorIndex,
  };
}

export type PingBin = 'lt50' | '50_80' | '80_120' | '120_200' | 'gt200' | 'unknown';

/**
 * Colorblind-friendlier ramp (blue → cyan → gold → vermillion → magenta),
 * inspired by Okabe–Ito / blue-orange sequences rather than pure green→red.
 * Radius adds a redundant non-color channel for the same bins.
 */
const PING_STYLES: Record<PingBin, { fill: string; stroke: string; radius: number; glyph: string }> = {
  lt50: { fill: '#0072B2', stroke: '#004b75', radius: 3, glyph: '●' },
  '50_80': { fill: '#56B4E9', stroke: '#2b7aa8', radius: 3.5, glyph: '◆' },
  '80_120': { fill: '#E69F00', stroke: '#9a6a00', radius: 4, glyph: '▲' },
  '120_200': { fill: '#D55E00', stroke: '#8f3e00', radius: 5, glyph: '■' },
  gt200: { fill: '#CC79A7', stroke: '#8a4f70', radius: 6, glyph: '✖' },
  unknown: { fill: '#999999', stroke: '#555555', radius: 3.5, glyph: '?' },
};

export function pingBin(ping: number): PingBin {
  if (!Number.isFinite(ping) || ping < 0) return 'unknown';
  if (ping < 50) return 'lt50';
  if (ping < 80) return '50_80';
  if (ping < 120) return '80_120';
  if (ping < 200) return '120_200';
  return 'gt200';
}

export function pingColor(ping: number): { bin: PingBin; fill: string; stroke: string; radius: number; glyph: string } {
  const bin = pingBin(ping);
  return { bin, ...PING_STYLES[bin] };
}

export const TRAJ_PAST_WEIGHT = 4.5;
export const TRAJ_FUTURE_WEIGHT = 2.5;
export const TRAJ_PAST_COLOR = '#0f8fa3';
export const TRAJ_FUTURE_COLOR = '#0f7285';
export const TRAJ_FUTURE_OPACITY = 0.35;
export const TRAJ_PAST_OPACITY = 0.95;
export const TRAJ_TIP_COLOR = '#ca8519';
export const TRAJ_DASH_ARRAY = '10 14';
export const TRAJ_DASH_ANIM_MS = 900;
export const FOCUS_PULSE_RADIUS = 14;
export const FOCUS_PULSE_COLOR = '#ca8519';
export const FOCUS_PULSE_WEIGHT = 2;
export const ARROW_SIZE_PX = 22;
export const ARROW_COLOR = '#ca8519';
export const ARROW_EDGE_SIZE_PX = 16;

/** Breath-highlight roles for focus / trajectory neighbors / expanded siblings. */
export type BreathRole = 'focus' | 'prev' | 'next' | 'sibling';

export const BREATH_COLORS: Record<BreathRole, { stroke: string; fill: string; radius: number; weight: number }> = {
  focus: { stroke: '#ca8519', fill: '#ca8519', radius: 14, weight: 2.5 },
  /** Past neighbor — cooler teal */
  prev: { stroke: '#0f8fa3', fill: '#0f8fa3', radius: 11, weight: 2 },
  /** Future neighbor — violet so it reads as "ahead" */
  next: { stroke: '#8b5cf6', fill: '#8b5cf6', radius: 11, weight: 2 },
  /** Other points lit when a cluster is expanded */
  sibling: { stroke: '#56B4E9', fill: '#56B4E9', radius: 9, weight: 1.75 },
};

export type BreathTarget<T> = {
  role: BreathRole;
  sample: T;
  key: string;
};

/**
 * Build breath-highlight targets for the current focus:
 * - always the focus sample
 * - chronological prev/next samples (same segment when available, else adjacent index)
 * - other markers currently expanded from the focus cluster as siblings
 */
export function collectBreathTargets<T extends TrajectoryPointLike>(
  samples: T[],
  focusKey: string,
  keyOf: (sample: T) => string,
  expandedKeys: Iterable<string> = [],
): BreathTarget<T>[] {
  if (!samples.length || !focusKey) return [];
  const focusIndex = samples.findIndex((s) => keyOf(s) === focusKey);
  if (focusIndex < 0) return [];

  const focus = samples[focusIndex]!;
  const targets: BreathTarget<T>[] = [{ role: 'focus', sample: focus, key: focusKey }];
  const claimed = new Set<string>([focusKey]);

  const prev = findTrajectoryNeighbor(samples, focusIndex, -1);
  if (prev) {
    const key = keyOf(prev);
    targets.push({ role: 'prev', sample: prev, key });
    claimed.add(key);
  }
  const next = findTrajectoryNeighbor(samples, focusIndex, 1);
  if (next) {
    const key = keyOf(next);
    targets.push({ role: 'next', sample: next, key });
    claimed.add(key);
  }

  for (const key of expandedKeys) {
    if (claimed.has(key)) continue;
    const sample = samples.find((s) => keyOf(s) === key);
    if (!sample) continue;
    targets.push({ role: 'sibling', sample, key });
    claimed.add(key);
  }
  return targets;
}

/** Prefer same-segment neighbor; fall back to index ±1. */
export function findTrajectoryNeighbor<T extends TrajectoryPointLike>(
  samples: T[],
  focusIndex: number,
  direction: -1 | 1,
): T | undefined {
  const focus = samples[focusIndex];
  if (!focus) return undefined;
  const target = focusIndex + direction;
  if (target < 0 || target >= samples.length) return undefined;
  // Walk in direction until same segment or take immediate neighbor.
  if (direction < 0) {
    for (let i = focusIndex - 1; i >= 0; i -= 1) {
      if (samples[i]!.segment_id === focus.segment_id) return samples[i];
    }
  } else {
    for (let i = focusIndex + 1; i < samples.length; i += 1) {
      if (samples[i]!.segment_id === focus.segment_id) return samples[i];
    }
  }
  return samples[target];
}
