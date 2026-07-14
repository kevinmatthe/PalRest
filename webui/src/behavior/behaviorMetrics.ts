// webui/src/behavior/behaviorMetrics.ts
import {
  D_IDLE,
  T_ACTIVE_CAP_MS,
  T_GAP_MS,
  V_IDLE,
  V_TRAVEL,
  type BehaviorClassMs,
  type BehaviorDominantClass,
  type BehaviorEdge,
  type BehaviorEdgeClass,
  type BehaviorPoint,
  type BehaviorSummary,
  type SummarizeBehaviorOptions,
} from './behaviorTypes';

function emptyClassMs(): BehaviorClassMs {
  return { stationary: 0, local: 0, traveling: 0 };
}

export function pickDominantClass(classMs: BehaviorClassMs): BehaviorDominantClass {
  const order: Array<'traveling' | 'local' | 'stationary'> = ['traveling', 'local', 'stationary'];
  let best: BehaviorDominantClass = 'unknown';
  let bestMs = 0;
  for (const key of order) {
    const ms = classMs[key];
    if (ms > bestMs) {
      bestMs = ms;
      best = key;
    }
  }
  return bestMs > 0 ? best : 'unknown';
}

function finitePoint(p: BehaviorPoint): boolean {
  return Number.isFinite(p.x) && Number.isFinite(p.y) && Number.isFinite(Date.parse(p.observed_at));
}

export function summarizeBehavior(
  samples: BehaviorPoint[],
  options: SummarizeBehaviorOptions = {},
): BehaviorSummary {
  const dIdle = options.dIdle ?? D_IDLE;
  const vIdle = options.vIdle ?? V_IDLE;
  const vTravel = options.vTravel ?? V_TRAVEL;
  const tGapMs = options.tGapMs ?? T_GAP_MS;
  const tActiveCapMs = options.tActiveCapMs ?? T_ACTIVE_CAP_MS;
  const includeEdges = options.includeEdges !== false;

  const sorted = [...samples]
    .filter(finitePoint)
    .sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at));

  const windowStartMs = options.windowStartMs ?? (sorted[0] ? Date.parse(sorted[0].observed_at) : 0);
  const windowEndMs =
    options.windowEndMs ?? (sorted.length ? Date.parse(sorted[sorted.length - 1]!.observed_at) : 0);
  const windowMs = Math.max(0, windowEndMs - windowStartMs);

  const classMs = emptyClassMs();
  let gapMs = 0;
  let pathLength = 0;
  let observedActiveMs = 0;
  let movingMs = 0;
  let peakSpeed = 0;
  const edges: BehaviorEdge[] = [];
  const segments = new Set<string>();

  for (const p of sorted) segments.add(p.segment_id);

  for (let i = 0; i < sorted.length - 1; i += 1) {
    const a = sorted[i]!;
    const b = sorted[i + 1]!;
    const ta = Date.parse(a.observed_at);
    const tb = Date.parse(b.observed_at);
    const dtMs = tb - ta;
    if (!(dtMs > 0)) continue;

    let edgeClass: BehaviorEdgeClass;
    let dist = 0;
    let speed = 0;

    if (a.segment_id !== b.segment_id || dtMs > tGapMs) {
      edgeClass = 'gap';
      gapMs += dtMs;
    } else {
      dist = Math.hypot(b.x - a.x, b.y - a.y);
      const dtS = dtMs / 1000;
      speed = dist / dtS;
      if (dist < dIdle || speed < vIdle) edgeClass = 'stationary';
      else if (speed >= vTravel) edgeClass = 'traveling';
      else edgeClass = 'local';

      const capped = Math.min(dtMs, tActiveCapMs);
      observedActiveMs += capped;
      pathLength += dist;
      if (edgeClass === 'local' || edgeClass === 'traveling') movingMs += capped;
      if (speed > peakSpeed) peakSpeed = speed;
      classMs[edgeClass] += capped;
    }

    if (includeEdges) {
      edges.push({
        fromIndex: i,
        toIndex: i + 1,
        class: edgeClass,
        dist,
        dtMs,
        speed: edgeClass === 'gap' ? 0 : speed,
      });
    }
  }

  const share = emptyClassMs();
  if (observedActiveMs > 0) {
    share.stationary = classMs.stationary / observedActiveMs;
    share.local = classMs.local / observedActiveMs;
    share.traveling = classMs.traveling / observedActiveMs;
  }

  let cx = 0;
  let cy = 0;
  for (const p of sorted) {
    cx += p.x;
    cy += p.y;
  }
  if (sorted.length) {
    cx /= sorted.length;
    cy /= sorted.length;
  }
  let radius = 0;
  for (const p of sorted) {
    radius = Math.max(radius, Math.hypot(p.x - cx, p.y - cy));
  }

  const movingSeconds = movingMs / 1000;
  const meanSpeed = movingSeconds > 0 ? pathLength / movingSeconds : 0;
  const sampleDensityPerHour =
    observedActiveMs > 0 ? sorted.length / (observedActiveMs / 3_600_000) : 0;

  return {
    sampleCount: sorted.length,
    segmentCount: segments.size,
    windowMs,
    observedActiveMs,
    pathLength,
    radius,
    meanSpeed,
    peakSpeed,
    sampleDensityPerHour,
    classMs,
    classShare: share,
    gapMs,
    gapShareOfWindow: windowMs > 0 ? gapMs / windowMs : 0,
    dominantClass: pickDominantClass(classMs),
    edges,
    poiDwells: [],
    teleportSuspects: [],
    poiHitRate: 0,
  };
}
