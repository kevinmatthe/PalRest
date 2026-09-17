import { describe, expect, it } from 'vitest';
import type { PlayerProgressResponse, PlayerTimelineResponse, ProgressChange, ProgressCheckpoint, TrajectorySample } from '../api';
import { summarizeJourney } from './journeySummary';
const base = Date.parse('2026-09-17T00:00:00Z');
const time = (s: number) => new Date(base + s * 1000).toISOString();
const point = (s: number, x = 100, extra: Partial<TrajectorySample> = {}): TrajectorySample => ({ user_id: 'u', observed_at: time(s), segment_id: 's', runtime_epoch: 1, source_ref: `p${s}`, x, y: 100, ping: 20, level: 10, ...extra });
const timeline = (trajectories: TrajectorySample[]): PlayerTimelineResponse => ({ user_id: 'u', trajectories, events: [], private_samples: [], trajectory_total: trajectories.length });
const cp = (id: number, s: number, value = 0, extra: Partial<ProgressCheckpoint> = {}): ProgressCheckpoint => ({ id, observed_at: time(s), captured_at: time(s), world_id: 'w', schema_version: 1, source: 'save_import', consistent: true, boundary: '', metrics: { owned_pals: { state: 'known', value }, capture_total: { state: 'known', value }, paldeck: { state: 'known', value }, fast_travel: { state: 'known', value } }, ...extra });
const diff = (a: ProgressCheckpoint, b: ProgressCheckpoint, extra: Partial<ProgressChange> = {}): ProgressChange => ({ id: b.id, previous_checkpoint_id: a.id, checkpoint_id: b.id, interval_start: a.observed_at, interval_end: b.observed_at, before: a.metrics.fast_travel.value!, after: b.metrics.fast_travel.value!, delta: b.metrics.fast_travel.value! - a.metrics.fast_travel.value!, metric: 'fast_travel', added: ['new'], removed: [], source: 'save_import', rule_version: 1, confidence: 'observed', ...extra });
const progress = (checkpoints: ProgressCheckpoint[], changes: ProgressChange[] = [], extra: Partial<PlayerProgressResponse> = {}): PlayerProgressResponse => ({ user_id: 'u', status: 'available', baseline: null, checkpoints, changes, checkpoint_total: checkpoints.length, change_total: changes.length, ...extra });
const summarize = (samples: TrajectorySample[] = [], data?: PlayerProgressResponse, end = 600, start = 0) => summarizeJourney({ userID: 'u', start: base + start * 1000, end: base + end * 1000, timeline: timeline(samples), progress: data });

describe('position evidence', () => {
  it('uses complete edges and includes leading/trailing unknown time without future interpolation', () => {
    const out = summarize([point(30), point(90, 6100), point(180, 12100)], undefined, 120);
    expect(out.position).toMatchObject({ sampleCount: 2, observedMs: 60000, unknownMs: 60000, movingMs: 60000, stationaryMs: 0, pathLength: 6000, coverage: 0.5, asOf: base + 90000, ageMs: 30000 });
    expect(out.position.edges[0].from.sourceRef).toBe('p30');
  });
  it.each([
    [point(0), point(30, NaN), point(60)], [point(0), point(30, 100, { observed_at: 'bad' }), point(60)],
    [point(0), point(30), point(30), point(60)], [point(0), point(30, 100, { user_id: 'other' }), point(60)],
    [point(0), point(60, 100, { runtime_epoch: 2 })], [point(0), point(60, 100, { segment_id: 'other' })],
    [point(0), point(301)], [point(0), point(60, 50100)],
  ])('preserves barriers (%#)', (...samples) => { expect(summarize(samples).position.observedMs).toBe(0); });
  it('does not use partial leading intervals or future invalid coordinates', () => {
    expect(summarize([point(-60), point(60), point(180, NaN)], undefined, 120).position.observedMs).toBe(0);
    expect(summarize([point(0), point(60), point(180, NaN)], undefined, 120).position.observedMs).toBe(60000);
  });
  it('integrates stationary time independently of sample density and uses grid centers', () => {
    const sparse = summarize([point(0), point(120)]).heat;
    const dense = summarize([point(0), point(30), point(60), point(90), point(120)]).heat;
    expect(sparse[0]).toMatchObject({ x: 5000, y: 5000, durationMs: 120000, start: base, end: base + 120000 });
    expect(dense[0].durationMs).toBe(sparse[0].durationMs); expect(sparse[0].edges).toHaveLength(1);
    expect(summarize([point(0, 9999), point(60, 10001)]).heat.map(c => c.durationMs)).toEqual([30000, 30000]);
  });
  it('reports levels only along the latest continuous positive-level chain', () => {
    const out = summarize([point(0, 100, { level: 5 }), point(60, 100, { level: 6 }), point(120, 100, { level: 0 }), point(180, 100, { level: 20 }), point(240, 100, { level: 22 })]);
    expect(out.position.level).toEqual({ from: 20, to: 22, delta: 2, start: base + 180000, end: base + 240000 });
    expect(summarize([point(0), point(60, 100, { level: 50, runtime_epoch: 2 })]).position.level).toBeNull();
  });
});

describe('persisted progress evidence', () => {
  it('keeps unknown distinct from verified zero and requires two checkpoints', () => {
    expect(summarize([], progress([cp(1, 0)])).metrics.fast_travel).toMatchObject({ status: 'unknown', delta: null, latestValue: 0 });
    expect(summarize([], progress([cp(1, 0), cp(2, 60)])).metrics.fast_travel).toMatchObject({ status: 'known', delta: 0 });
    const unknown = cp(2, 60); unknown.metrics.fast_travel = { state: 'unknown' };
    expect(summarize([], progress([cp(1, 0), unknown])).metrics.fast_travel).toMatchObject({ status: 'unknown', delta: null, latestValue: null });
  });
  it('attributes only complete saved intervals and hides future checkpoints', () => {
    const a = cp(1, -60), b = cp(2, 60, 1), c = cp(3, 180, 2);
    const metric = summarize([], progress([b, c], [diff(a, b), diff(b, c)], { baseline: a }), 120).metrics.fast_travel;
    expect(metric).toMatchObject({ delta: null, latestValue: 1, changes: [] });
    expect(metric.runs.flat().map(p => p.checkpointID)).toEqual([2]);
  });
  it('sums saved changes and preserves checkpoint trend values', () => {
    const a = cp(1, 0), b = cp(2, 60, 2), metric = summarize([], progress([a, b], [diff(a, b)])).metrics.fast_travel;
    expect(metric).toMatchObject({ status: 'known', delta: 2 }); expect(metric.changes.map(c => c.id)).toEqual([2]);
    expect(metric.runs).toEqual([[{ checkpointID: 1, time: base, value: 0 }, { checkpointID: 2, time: base + 60000, value: 2 }]]);
  });
  it('never substitutes raw differences for absent, mismatched or orphan changes', () => {
    const a = cp(1, 0), b = cp(2, 60, 2);
    for (const changes of [[], [diff(a, b, { before: 99 })], [diff(a, b, { previous_checkpoint_id: 999 })]]) {
      expect(summarize([], progress([a, b], changes)).metrics.fast_travel.delta).toBeNull();
      expect(summarize([], progress([a, b], changes)).metrics.fast_travel.changes).toEqual([]);
    }
  });
  it.each(['world_changed', 'schema_changed', 'counter_reset', 'inconsistent', 'out_of_order', 'after_out_of_order', 'replayed_snapshot'])('refuses net delta across %s', boundary => {
    const a = cp(1, 0), b = cp(2, 60, 1), c = cp(3, 120, 0, { boundary });
    const out = summarize([], progress([a, b, c], [diff(a, b)]));
    expect(out.metrics.fast_travel).toMatchObject({ status: 'boundary', delta: null });
    expect(out.metrics.fast_travel.runs.map(r => r.length)).toEqual([2, 1]); expect(out.warnings).toContain('progress_boundary');
  });
  it('detects world/schema changes without explicit labels and allows initial baseline', () => {
    const a = cp(1, 0, 0, { boundary: 'baseline' }), b = cp(2, 60, 1);
    expect(summarize([], progress([a, b], [diff(a, b)])).metrics.fast_travel.status).toBe('known');
    b.world_id = 'other'; expect(summarize([], progress([a, b], [diff(a, b)])).metrics.fast_travel.status).toBe('boundary');
    b.world_id = 'w'; b.boundary = 'baseline';
    expect(summarize([], progress([a, b], [diff(a, b)])).metrics.fast_travel.runs.map(r => r.length)).toEqual([1, 1]);
  });
  it('breaks trends at unknown values and truncation', () => {
    const a = cp(1, 0), b = cp(2, 60), c = cp(3, 120, 2); b.metrics.fast_travel = { state: 'unsupported' };
    expect(summarize([], progress([a, b, c])).metrics.fast_travel.runs.map(r => r.length)).toEqual([1, 1]);
    const end = cp(2, 60, 1), out = summarize([], progress([a, end], [diff(a, end)], { checkpoint_total: 3 }));
    expect(out.metrics.fast_travel).toMatchObject({ status: 'partial', delta: 1 }); expect(out.metrics.fast_travel.runs.map(r => r.length)).toEqual([1, 1]);
    expect(out.warnings).toContain('progress_truncated');
    expect(summarize([], progress([a, cp(2, 60)], [], { change_total: 1 })).metrics.fast_travel.delta).toBeNull();
  });
  it('rejects progress belonging to another player', () => {
    expect(summarize([], progress([cp(1, 0), cp(2, 60)], [], { user_id: 'other' })).metrics.fast_travel).toMatchObject({ status: 'unknown', delta: null, runs: [] });
  });
});

describe('conservative exploration inference', () => {
  const samples = [point(0, 0), point(120, 12000), point(300, 12000)];
  const a = cp(1, 0), b = cp(2, 300, 1), data = progress([a, b], [diff(a, b)]);
  it('requires all evidence thresholds and retains auditable rule evidence', () => {
    const out = summarize(samples, data, 500); expect(out.inferences).toHaveLength(1);
    expect(out.inferences[0]).toMatchObject({ kind: 'exploration', ruleVersion: 1, changeIDs: [2], observedMs: 300000, movingMs: 120000, coverage: 0.6, unlockedCount: 1 });
    expect(out.inferences[0].edges).toEqual(out.position.edges);
  });
  it('withholds conclusions for insufficient duration, coverage, movement, unlocks or any truncation', () => {
    expect(summarize(samples, data, 501).inferences).toEqual([]); expect(summarize(samples.slice(0, 2), data, 300).inferences).toEqual([]);
    expect(summarize(samples.map(p => ({ ...p, x: 0 })), data, 300).inferences).toEqual([]);
    expect(summarize(samples, progress([a, cp(2, 300)]), 300).inferences).toEqual([]);
    expect(summarize(samples, { ...data, change_total: 2 }, 300).inferences).toEqual([]);
    expect(summarizeJourney({ userID: 'u', start: base, end: base + 300000, timeline: { ...timeline(samples), trajectory_total: 10 }, progress: data }).inferences).toEqual([]);
  });
  it('warns on unavailable and invalid input', () => {
    const out = summarizeJourney({ userID: 'u', start: base, end: base + 600000 });
    expect(out.warnings).toContain('data_unavailable'); expect(out.position.unknownMs).toBe(600000);
    expect(summarizeJourney({ userID: 'u', start: NaN, end: base }).warnings).toContain('invalid_input');
  });
});

it('does not present an earlier level chain as current after an unknown or isolated restart sample', () => {
  expect(summarize([point(0), point(60, 100, { level: 11 }), point(120, 100, { level: 0 })]).position.level).toBeNull();
  expect(summarize([point(0), point(60, 100, { level: 11 }), point(120, 100, { runtime_epoch: 2, level: 20 })]).position.level).toBeNull();
  expect(summarize([point(0), point(60, 100, { level: 11 }), point(180, 100, { runtime_epoch: 2 })], undefined, 120).position.level?.delta).toBe(1);
});
it('does not infer negative cumulative activity from an unlabeled rollback', () => {
  const a = cp(1, 0, 10), b = cp(2, 60, 2);
  expect(summarize([], progress([a, b], [diff(a, b)])).metrics.fast_travel).toMatchObject({ status: 'boundary', delta: null });
});
it('retains an invalid checkpoint timestamp as uncertainty instead of connecting over it', () => {
  const a = cp(1, 0), b = cp(2, 30, 0, { observed_at: 'invalid' }), c = cp(3, 60);
  const metric = summarize([], progress([a, b, c])).metrics.fast_travel;
  expect(metric.delta).toBeNull(); expect(metric.runs.map(r => r.length)).toEqual([1, 1]);
});
it('allows negative owned-pals changes without treating them as cumulative counter resets', () => {
  const a = cp(1, 0), b = cp(2, 60); a.metrics.owned_pals.value = 10; b.metrics.owned_pals.value = 8;
  const change = diff(a, b, { metric: 'owned_pals', before: 10, after: 8, delta: -2 });
  expect(summarize([], progress([a, b], [change])).metrics.owned_pals).toMatchObject({ status: 'known', delta: -2 });
});
it('keeps known local intervals partial around missing observations', () => {
  const a = cp(1, 0), b = cp(2, 60, 1), c = cp(3, 120, 1); c.metrics.fast_travel = { state: 'unknown' };
  expect(summarize([], progress([a, b, c], [diff(a, b)])).metrics.fast_travel).toMatchObject({ status: 'partial', delta: 1 });
});

it.each([40, 50, 100])('classifies a nonzero constant %i units/s path independently of sampling density', speed => {
  const sparse = summarize([point(0, 0), point(10, speed * 10)], undefined, 10);
  const dense = summarize([0, 2, 4, 6, 8, 10].map(seconds => point(seconds, speed * seconds)), undefined, 10);
  expect(dense.position.movingMs).toBe(sparse.position.movingMs);
  expect(dense.position.stationaryMs).toBe(sparse.position.stationaryMs);
  expect(dense.heat.reduce((sum, cell) => sum + cell.durationMs, 0)).toBe(sparse.heat.reduce((sum, cell) => sum + cell.durationMs, 0));
  expect(dense.position.movingMs).toBe(speed < 50 ? 0 : 10000);
});
it('does not lose exploration evidence when the same movement is sampled more densely', () => {
  const a = cp(1, 0), b = cp(2, 300, 1), data = progress([a, b], [diff(a, b)]);
  const sparse = summarize([point(0, 0), point(120, 12000), point(300, 12000)], data, 300);
  const dense = summarize(Array.from({ length: 151 }, (_, i) => point(i * 2, Math.min(i * 2, 120) * 100)), data, 300);
  expect(sparse.inferences).toHaveLength(1);
  expect(dense.inferences).toHaveLength(1);
  expect(dense.inferences[0].movingMs).toBe(sparse.inferences[0].movingMs);
  expect(dense.position.stationaryMs).toBe(sparse.position.stationaryMs);
});


describe('saved growth and milestone evidence', () => {
  const growth = (id: number, s: number, level: number, experience: number) => {
    const checkpoint = cp(id, s);
    Object.assign(checkpoint.metrics, { level: { state: 'known', value: level }, experience: { state: 'known', value: experience } });
    return checkpoint;
  };
  const growthDiff = (a: ProgressCheckpoint, b: ProgressCheckpoint, metric: 'level' | 'experience') => diff(a, b, {
    metric, before: a.metrics[metric]!.value!, after: b.metrics[metric]!.value!,
    delta: b.metrics[metric]!.value! - a.metrics[metric]!.value!, added: [], removed: [],
  });
  it('keeps old four-metric responses unknown for growth while retaining genuine zero experience', () => {
    const old = summarize([], progress([cp(1, 0), cp(2, 60)]));
    expect(old.metrics.level).toMatchObject({ status: 'unknown', delta: null, latestValue: null, runs: [] });
    expect(old.metrics.experience).toMatchObject({ status: 'unknown', delta: null, latestValue: null });
    const a = growth(1, 0, 1, 0), b = growth(2, 60, 1, 0);
    expect(summarize([], progress([a, b])).metrics.experience).toMatchObject({ status: 'known', delta: 0, latestValue: 0 });
  });
  it('plots saved level and experience independently of REST levels and excludes future milestones', () => {
    const a = growth(1, 0, 1, 0), b = growth(2, 60, 2, 100), c = growth(3, 180, 3, 300);
    const changes = [growthDiff(a, b, 'level'), growthDiff(a, b, 'experience'), growthDiff(b, c, 'level')];
    const out = summarize([point(0, 100, { level: 50 }), point(60, 100, { level: 51 })], progress([a, b, c], changes), 120);
    expect(out.metrics.level).toMatchObject({ status: 'known', delta: 1, latestValue: 2 });
    expect(out.metrics.experience).toMatchObject({ status: 'known', delta: 100, latestValue: 100 });
    expect(out.metrics.experience?.runs.flat().map(p => p.value)).toEqual([0, 100]);
    expect(out.milestones).toEqual([changes[0]]);
  });
  it.each(['level', 'experience'] as const)('breaks comparison on an unlabeled %s regression while retaining valid earlier milestones', key => {
    const a = growth(1, 0, 10, 1000), b = growth(2, 60, 11, 1100), c = growth(3, 120, 11, 1100);
    c.metrics[key]!.value = 1;
    const valid = growthDiff(a, b, 'level'), reset = growthDiff(b, c, key);
    const out = summarize([], progress([a, b, c], [valid, reset]));
    expect(out.metrics[key]).toMatchObject({ status: 'boundary', delta: null });
    expect(out.metrics[key]?.runs.at(-1)).toHaveLength(1);
    expect(out.milestones).toEqual([valid]);
  });
  it('includes only evidenced new unlock IDs and excludes cross-schema milestone claims', () => {
    const a = cp(1, 0), b = cp(2, 60, 1), c = cp(3, 120, 2, { schema_version: 2 });
    a.metrics.fast_travel.ids = []; b.metrics.fast_travel.ids = ['Travel_A']; c.metrics.fast_travel.ids = ['Travel_A', 'Travel_B'];
    const valid = diff(a, b, { added: ['Travel_A'] }), cross = diff(b, c, { added: ['Travel_B'] });
    expect(summarize([], progress([a, b, c], [valid, cross])).milestones).toEqual([valid]);
    expect(summarize([], progress([a, b], [diff(a, b, { added: ['invented'] })])).milestones).toEqual([]);
    delete b.metrics.fast_travel.ids;
    expect(summarize([], progress([a, b], [valid])).milestones).toEqual([]);
  });
  it('keeps verified local milestones under truncation without claiming the whole window', () => {
    const a = growth(1, 0, 1, 0), b = growth(2, 60, 2, 100), change = growthDiff(a, b, 'level');
    const out = summarize([], progress([a, b], [change], { change_total: 50 }));
    expect(out.metrics.level).toMatchObject({ status: 'partial', delta: 1 });
    expect(out.milestones).toEqual([change]);
    expect(summarize([], progress([a, b], [] )).milestones).toEqual([]);
  });
});

it('preserves legacy coordinates without deriving path, duration, heat or activities', () => {
  const out = summarize([point(0, 1000, { runtime_epoch: undefined }), point(120, 13000, { runtime_epoch: undefined }), point(300, 13000, { runtime_epoch: undefined })]);
  expect(out.position).toMatchObject({ sampleCount: 3, observedMs: 0, movingMs: 0, pathLength: 0, lastObservation: { x: 13000, y: 100 }, asOf: base + 300000 });
  expect(out.heat).toEqual([]);
  expect(out.inferences).toEqual([]);
  expect(out.warnings).toContain('position_continuity_unknown');
});
it('does not turn legacy equal counts without set details into verified unchanged sets', () => {
  const a = cp(1, 0, 4), b = cp(2, 60, 4);
  const out = summarize([], progress([a, b]));
  for (const key of ['owned_pals', 'paldeck', 'fast_travel'] as const) {
    expect(out.metrics[key]).toMatchObject({ latestValue: 4, delta: null, status: 'unknown' });
    expect(out.metrics[key].runs.map(run => run.length)).toEqual([1, 1]);
  }
  expect(out.metrics.capture_total).toMatchObject({ latestValue: 4, delta: 0 });
});
it('does not manufacture a rollback when legacy set details disappear', () => {
  const a = cp(1, 0, 1), b = cp(2, 60, 1);
  a.metrics.fast_travel.ids = ['Travel_A'];
  const out = summarize([], progress([a, b]));
  expect(out.metrics.fast_travel).toMatchObject({ status: 'unknown', latestValue: 1, delta: null });
  expect(out.warnings).not.toContain('progress_boundary');
});
it('retains saved legacy numeric diffs without crashing or inventing detailed milestones', () => {
  const a = cp(1, 0), b = cp(2, 60, 2);
  const change = diff(a, b);
  delete (change as Partial<ProgressChange>).added;
  delete (change as Partial<ProgressChange>).removed;
  const out = summarize([], progress([a, b], [change]));
  expect(out.metrics.fast_travel).toMatchObject({ delta: 2, latestValue: 2 });
  expect(out.milestones).toEqual([]);
});
