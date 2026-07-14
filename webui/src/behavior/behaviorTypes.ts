/** Calibration: change-based sampling; tune against live trajectory density. */

export const D_IDLE = 500;
export const V_IDLE = 50;
export const V_TRAVEL = 800;
export const T_GAP_MS = 5 * 60_000;
export const T_ACTIVE_CAP_MS = T_GAP_MS;
export const GAP_SHARE_WARN = 0.15;

export type BehaviorPoint = {
  observed_at: string;
  segment_id: string;
  x: number;
  y: number;
  ping?: number;
};

export type BehaviorEdgeClass = 'stationary' | 'local' | 'traveling' | 'gap';
export type BehaviorDominantClass = 'stationary' | 'local' | 'traveling' | 'unknown';

export type BehaviorEdge = {
  fromIndex: number;
  toIndex: number;
  class: BehaviorEdgeClass;
  dist: number;
  dtMs: number;
  speed: number;
};

export type BehaviorClassMs = {
  stationary: number;
  local: number;
  traveling: number;
};

export type BehaviorSummary = {
  sampleCount: number;
  segmentCount: number;
  windowMs: number;
  observedActiveMs: number;
  pathLength: number;
  radius: number;
  meanSpeed: number;
  peakSpeed: number;
  sampleDensityPerHour: number;
  classMs: BehaviorClassMs;
  classShare: BehaviorClassMs;
  gapMs: number;
  gapShareOfWindow: number;
  dominantClass: BehaviorDominantClass;
  edges: BehaviorEdge[];
};

export type SummarizeBehaviorOptions = {
  windowStartMs?: number;
  windowEndMs?: number;
  dIdle?: number;
  vIdle?: number;
  vTravel?: number;
  tGapMs?: number;
  tActiveCapMs?: number;
  includeEdges?: boolean;
};
