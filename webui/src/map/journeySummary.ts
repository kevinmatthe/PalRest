import type { PlayerProgressResponse, ProgressChange, ProgressCheckpoint, ProgressMetricName } from '../api';
import { T_GAP_MS, V_IDLE } from '../behavior/behaviorTypes';
import { connectionBreak, prepareTrajectory } from './workspacePlayback';
import type { JourneyEdge, JourneyHeatCell, JourneyInput, JourneyMetric, JourneySummary } from './journeyTypes';
export type * from './journeyTypes';

const METRICS: ProgressMetricName[] = ['level', 'experience', 'owned_pals', 'capture_total', 'paldeck', 'fast_travel'];
const HEAT_GRID = 10_000;
const emptyMetric = (): JourneyMetric => ({ status: 'unknown', delta: null, latestValue: null, changes: [], runs: [] });
const metricValue = (checkpoint: ProgressCheckpoint | undefined, key: ProgressMetricName): number | null => {
  const metric = checkpoint?.metrics[key];
  return metric?.state === 'known' && Number.isFinite(metric.value) && metric.value! >= (key === 'level' ? 1 : 0) && Number.isSafeInteger(metric.value) ? metric.value! : null;
};

/** A boundary belongs to the incoming interval; an initial baseline is not a reset. */
export function progressBoundary(previous: ProgressCheckpoint | undefined, current: ProgressCheckpoint): boolean {
  if (!current.consistent || !current.world_id || (current.boundary && current.boundary !== 'baseline')) return true;
  return !!previous && (previous.world_id !== current.world_id || previous.schema_version !== current.schema_version ||
    !previous.consistent || Date.parse(current.observed_at) <= Date.parse(previous.observed_at) ||
    (['level', 'experience', 'capture_total', 'paldeck', 'fast_travel'] as ProgressMetricName[]).some(key => {
      const before = metricValue(previous, key), after = metricValue(current, key);
      return before !== null && after !== null && (after < before ||
        ((key === 'paldeck' || key === 'fast_travel') && (previous.metrics[key]?.ids ?? []).some(id => !(current.metrics[key]?.ids ?? []).includes(id))));
    }));
}

function compatible(previous: ProgressCheckpoint, current: ProgressCheckpoint): boolean {
  return !progressBoundary(previous, current) && !current.boundary &&
    !['inconsistent', 'out_of_order', 'world_unknown'].includes(previous.boundary);
}

function unchanged(a: ProgressCheckpoint, b: ProgressCheckpoint, key: ProgressMetricName): boolean {
  if (metricValue(a, key) !== metricValue(b, key)) return false;
  const left = a.metrics[key]?.ids ?? [], right = b.metrics[key]?.ids ?? [];
  return left.length === right.length && left.every(id => right.includes(id));
}

function verifiedChange(change: ProgressChange, a: ProgressCheckpoint, b: ProgressCheckpoint, key: ProgressMetricName): boolean {
  return change.metric === key && change.previous_checkpoint_id === a.id && change.checkpoint_id === b.id &&
    Date.parse(change.interval_start) === Date.parse(a.observed_at) && Date.parse(change.interval_end) === Date.parse(b.observed_at) &&
    change.before === metricValue(a, key) && change.after === metricValue(b, key) &&
    Number.isFinite(change.delta) && change.delta === change.after - change.before &&
    change.rule_version === 1 && change.confidence === 'observed' && change.source === 'save_import';
}

function summarizeProgress(data: PlayerProgressResponse, start: number, end: number, truncated: boolean): Record<ProgressMetricName, JourneyMetric> {
  const invalidTimes = [data.baseline, ...data.checkpoints].some(p => p && !Number.isFinite(Date.parse(p.observed_at)));
  // Keep only real checkpoint identities; baseline is sometimes also in the window array.
  const all = [...new Map([data.baseline, ...data.checkpoints].filter((p): p is ProgressCheckpoint => p !== null)
    .filter(p => Date.parse(p.observed_at) <= end).map(p => [p.id, p])).values()]
    .sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at) || a.id - b.id);
  const points = all.filter(p => Date.parse(p.observed_at) >= start);
  const hasBoundary = points.some(p => progressBoundary(all[all.indexOf(p) - 1], p));
  const result = {} as Record<ProgressMetricName, JourneyMetric>;
  for (const key of METRICS) {
    const metric = emptyMetric();
    metric.latestValue = metricValue(points.at(-1), key);
    let verifiedIntervals = 0;
    let incomplete = truncated || invalidTimes || points.some(p => metricValue(p, key) === null);
    let sum = 0;
    for (let i = 0; i < points.length; i++) {
      const current = points[i], previous = points[i - 1];
      const value = metricValue(current, key);
      const connected = !invalidTimes && previous && compatible(previous, current) && metricValue(previous, key) !== null && value !== null;
      const matching = connected ? data.changes.filter(c => verifiedChange(c, previous, current, key)) : [];
      // More than one saved diff for a pair is ambiguous; never double count it.
      const verified = matching.length === 1;
      const zero = connected && !truncated && unchanged(previous, current, key) && matching.length === 0 &&
        !data.changes.some(c => c.metric === key && c.checkpoint_id === current.id && c.previous_checkpoint_id === previous.id);
      if (previous) {
        if (verified || zero) { verifiedIntervals++; sum += verified ? matching[0].delta : 0; }
        else incomplete = true;
      }
      if (verified) metric.changes.push(matching[0]);
      if (value !== null) {
        // Missing diff for a changed pair also prevents a misleading growth segment.
        if (!connected || truncated || (!verified && !zero)) metric.runs.push([]);
        metric.runs[metric.runs.length - 1].push({ checkpointID: current.id, time: Date.parse(current.observed_at), value });
      }
    }
    if (hasBoundary) { metric.status = 'boundary'; metric.reason = 'progress_boundary'; }
    else if (verifiedIntervals > 0) { metric.status = incomplete ? 'partial' : 'known'; metric.delta = sum; }
    else { metric.status = truncated ? 'partial' : 'unknown'; }
    if (metric.status === 'partial') metric.reason = truncated ? 'progress_truncated' : 'insufficient_observations';
    result[key] = metric;
  }
  return result;
}

/** Milestones name saved increases, never guessed thresholds or precise event times. */
function growthMilestones(metrics: JourneySummary['metrics'], data: PlayerProgressResponse): ProgressChange[] {
  const checkpoints = new Map([data.baseline, ...data.checkpoints].filter((p): p is ProgressCheckpoint => p !== null).map(p => [p.id, p]));
  return (['level', 'paldeck', 'fast_travel'] as const).flatMap(key => (metrics[key]?.changes ?? []).filter(change => {
    if (change.delta <= 0 || change.removed.length) return false;
    if (key === 'level') return change.added.length === 0;
    const before = checkpoints.get(change.previous_checkpoint_id)?.metrics[key]?.ids;
    const after = checkpoints.get(change.checkpoint_id)?.metrics[key]?.ids;
    if (!before || !after || before.length !== change.before || after.length !== change.after) return false;
    const oldIDs = new Set(before), newIDs = new Set(after), added = new Set(change.added);
    const actualAdded = after.filter(id => !oldIDs.has(id));
    return oldIDs.size === before.length && newIDs.size === after.length && added.size === change.added.length &&
      before.every(id => newIDs.has(id)) && actualAdded.length === change.delta && added.size === actualAdded.length &&
      actualAdded.every(id => added.has(id));
  })).sort((a, b) => Date.parse(b.interval_end) - Date.parse(a.interval_end) || b.id - a.id);
}

function heatCells(edges: JourneyEdge[]): JourneyHeatCell[] {
  const cells = new Map<string, JourneyHeatCell>();
  for (const edge of edges) {
    if (!edge.stationary) continue;
    for (const point of [edge.from, edge.to]) {
      const gx = Math.floor(point.x / HEAT_GRID), gy = Math.floor(point.y / HEAT_GRID), id = `${gx}:${gy}`;
      let cell = cells.get(id);
      if (!cell) { cell = { id, x: (gx + 0.5) * HEAT_GRID, y: (gy + 0.5) * HEAT_GRID, durationMs: 0, start: edge.start, end: edge.end, edges: [] }; cells.set(id, cell); }
      cell.durationMs += edge.durationMs / 2;
      cell.start = Math.min(cell.start, edge.start); cell.end = Math.max(cell.end, edge.end);
      if (cell.edges.at(-1) !== edge) cell.edges.push(edge);
    }
  }
  return [...cells.values()].sort((a, b) => b.durationMs - a.durationMs || a.id.localeCompare(b.id));
}

/** Summarize evidence already observed by the cursor; never estimate unobserved tails. */
export function summarizeJourney(input: JourneyInput): JourneySummary {
  const valid = !!input.userID && Number.isFinite(input.start) && Number.isFinite(input.end) && input.end >= input.start;
  const start = Number.isFinite(input.start) ? input.start : 0;
  const end = valid ? input.end : start;
  const out: JourneySummary = {
    start, end,
    position: { sampleCount: 0, totalCount: 0, observedMs: 0, unknownMs: end - start, coverage: 0, movingMs: 0, stationaryMs: 0, pathLength: 0, asOf: null, ageMs: null, edges: [], level: null },
    metrics: { level: emptyMetric(), experience: emptyMetric(), owned_pals: emptyMetric(), capture_total: emptyMetric(), paldeck: emptyMetric(), fast_travel: emptyMetric() }, milestones: [], heat: [], inferences: [], warnings: [],
  };
  if (!valid) { out.warnings.push('invalid_input'); return out; }
  const timeline = input.timeline?.user_id === input.userID ? input.timeline : undefined;
  const progress = input.progress?.user_id === input.userID && input.progress.status === 'available' ? input.progress : undefined;
  if (!timeline || !progress) out.warnings.push('data_unavailable');
  const timelineTruncated = !!timeline && (timeline.trajectory_total ?? timeline.trajectories.length) > timeline.trajectories.length;
  const progressTruncated = !!progress && (progress.checkpoint_total > progress.checkpoints.length || progress.change_total > progress.changes.length);
  if (timelineTruncated) out.warnings.push('timeline_truncated');
  if (progressTruncated) out.warnings.push('progress_truncated');
  // Preparing BEFORE window/player filtering preserves invalid observations and interleaved-player barriers.
  const prepared = prepareTrajectory(timeline?.trajectories ?? []);
  const visible = prepared.filter(p => p.user_id === input.userID && p.time >= start && p.time <= end);
  const position = out.position;
  position.sampleCount = visible.length;
  position.totalCount = timeline?.trajectory_total ?? timeline?.trajectories.length ?? 0;
  position.asOf = visible.at(-1)?.time ?? null;
  position.ageMs = position.asOf === null ? null : end - position.asOf;
  let currentLevel: JourneySummary['position']['level'] = null;
  for (let i = 1; i < prepared.length; i++) {
    const a = prepared[i - 1], b = prepared[i];
    if (b.time > end) break;
    if (a.user_id !== input.userID || b.user_id !== input.userID || a.time < start || connectionBreak(a, b)) {
      currentLevel = null; position.level = null; continue;
    }
    const durationMs = b.time - a.time, distance = Math.hypot(b.x - a.x, b.y - a.y);
    // Time-normalized classification keeps constant-speed paths independent of sampling density.
    const stationary = distance / (durationMs / 1000) < V_IDLE;
    position.edges.push({ start: a.time, end: b.time, durationMs, distance, stationary, from: { x: a.x, y: a.y, sourceRef: a.source_ref }, to: { x: b.x, y: b.y, sourceRef: b.source_ref } });
    position.observedMs += durationMs; position.pathLength += distance;
    if (stationary) position.stationaryMs += durationMs; else position.movingMs += durationMs;
    if (Number.isFinite(a.level) && a.level > 0 && Number.isFinite(b.level) && b.level > 0) {
      const previousLevel = currentLevel as JourneySummary['position']['level'];
      currentLevel = previousLevel && previousLevel.end === a.time
        ? { ...previousLevel, to: b.level, delta: b.level - previousLevel.from, end: b.time }
        : { from: a.level, to: b.level, delta: b.level - a.level, start: a.time, end: b.time };
      position.level = currentLevel;
    } else { currentLevel = null; position.level = null; }
  }
  position.unknownMs = Math.max(0, end - start - position.observedMs);
  position.coverage = end > start ? position.observedMs / (end - start) : 0;
  if (position.observedMs < 300_000 || position.coverage < 0.6) out.warnings.push('insufficient_observations');
  if (position.ageMs !== null && position.ageMs > T_GAP_MS) out.warnings.push('stale_positions');
  out.heat = heatCells(position.edges);
  if (progress) out.metrics = summarizeProgress(progress, start, end, progressTruncated);
  if (progress) out.milestones = growthMilestones(out.metrics, progress);
  const boundary = METRICS.some(key => out.metrics[key]?.status === 'boundary');
  if (boundary) out.warnings.push('progress_boundary');
  const travel = out.metrics.fast_travel;
  if (timeline && progress && !timelineTruncated && !progressTruncated && !boundary && travel.status === 'known' &&
    position.observedMs >= 300_000 && position.coverage >= 0.6 && position.movingMs >= 120_000 && travel.delta !== null && travel.delta >= 1) {
    out.inferences.push({ kind: 'exploration', ruleVersion: 1, changeIDs: travel.changes.filter(c => c.delta > 0).map(c => c.id), edges: position.edges,
      observedMs: position.observedMs, movingMs: position.movingMs, coverage: position.coverage, unlockedCount: travel.delta });
  }
  return out;
}
