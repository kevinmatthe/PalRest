# Trajectory Behavior Analysis (Phase A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a pure behavior-metrics library and a timeline「行为摘要」panel that summarizes traveling/local/idle mix, radius, path length, speeds, and density from loaded trajectory samples.

**Architecture:** Zero-DOM pure functions under `webui/src/behavior/`; React panel `BehaviorSummaryPanel`; `PlayerTimeline` memoizes `summarizeBehavior(samples, window)` and renders the panel under `TimelineMap`. No backend endpoints in phase A.

**Tech Stack:** TypeScript, React 19, Vitest, Testing Library.

**Spec:** `docs/superpowers/specs/2026-07-14-trajectory-behavior-analysis-design.md`

---

## File Structure

| File | Role |
|------|------|
| Create `webui/src/behavior/behaviorTypes.ts` | Types + threshold constants |
| Create `webui/src/behavior/behaviorMetrics.ts` | `summarizeBehavior` pure core |
| Create `webui/src/behavior/behaviorMetrics.test.ts` | Algorithm fixtures |
| Create `webui/src/behavior/behaviorFormat.ts` | zh labels + number formatting |
| Create `webui/src/behavior/behaviorFormat.test.ts` | Format tests |
| Create `webui/src/components/BehaviorSummaryPanel.tsx` | UI card |
| Create `webui/src/components/BehaviorSummaryPanel.test.tsx` | Component tests |
| Modify `webui/src/components/PlayerTimeline.tsx` | Wire samples → summary → panel |
| Modify `webui/src/styles.css` | Panel / mix-bar styles |

---

### Task 1: Types and constants

**Files:**
- Create: `webui/src/behavior/behaviorTypes.ts`
- Create: `webui/src/behavior/behaviorMetrics.test.ts` (constants smoke only first)

- [ ] **Step 1: Write failing constants test**

```ts
// webui/src/behavior/behaviorMetrics.test.ts
import { describe, expect, it } from 'vitest';
import {
  D_IDLE,
  T_ACTIVE_CAP_MS,
  T_GAP_MS,
  V_IDLE,
  V_TRAVEL,
} from './behaviorTypes';

describe('behavior thresholds', () => {
  it('exports design defaults', () => {
    expect(D_IDLE).toBe(500);
    expect(V_IDLE).toBe(50);
    expect(V_TRAVEL).toBe(800);
    expect(T_GAP_MS).toBe(5 * 60_000);
    expect(T_ACTIVE_CAP_MS).toBe(T_GAP_MS);
  });
});
```

- [ ] **Step 2: Run test — expect FAIL (module missing)**

Run: `cd webui && npm test -- src/behavior/behaviorMetrics.test.ts`

- [ ] **Step 3: Implement types + constants**

```ts
// webui/src/behavior/behaviorTypes.ts
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
```

Note: `classShare` uses the same shape as `classMs` but values are 0–1 fractions.

- [ ] **Step 4: Run test — PASS**

- [ ] **Step 5: Commit**

```bash
git add webui/src/behavior/behaviorTypes.ts webui/src/behavior/behaviorMetrics.test.ts
git commit -m "feat(webui): add behavior analysis types and thresholds"
```

---

### Task 2: `summarizeBehavior` core (TDD)

**Files:**
- Create: `webui/src/behavior/behaviorMetrics.ts`
- Modify: `webui/src/behavior/behaviorMetrics.test.ts`

- [ ] **Step 1: Append failing algorithm tests**

```ts
import { summarizeBehavior } from './behaviorMetrics';
import type { BehaviorPoint } from './behaviorTypes';

function pt(
  partial: Partial<BehaviorPoint> & Pick<BehaviorPoint, 'observed_at' | 'segment_id' | 'x' | 'y'>,
): BehaviorPoint {
  return { ping: 40, ...partial };
}

const t0 = Date.parse('2026-07-14T10:00:00.000Z');

describe('summarizeBehavior', () => {
  it('returns safe zeros for empty or single point', () => {
    expect(summarizeBehavior([], { windowStartMs: t0, windowEndMs: t0 + 3600_000 }).dominantClass).toBe('unknown');
    const one = summarizeBehavior(
      [pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 })],
      { windowStartMs: t0, windowEndMs: t0 + 3600_000 },
    );
    expect(one.sampleCount).toBe(1);
    expect(one.pathLength).toBe(0);
    expect(one.dominantClass).toBe('unknown');
  });

  it('classifies stationary pair with negligible movement', () => {
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:02:00.000Z', segment_id: 's1', x: 10, y: 0 }),
    ];
    const s = summarizeBehavior(samples, {
      windowStartMs: t0,
      windowEndMs: t0 + 10 * 60_000,
    });
    expect(s.classShare.stationary).toBeGreaterThan(0.99);
    expect(s.dominantClass).toBe('stationary');
    expect(s.pathLength).toBeLessThan(20);
    expect(s.peakSpeed).toBeLessThan(50);
  });

  it('classifies high-speed travel', () => {
    // 100_000 world units in 60s → ~1667 u/s >= V_travel 800
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's1', x: 100_000, y: 0 }),
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 5 * 60_000 });
    expect(s.dominantClass).toBe('traveling');
    expect(s.pathLength).toBeCloseTo(100_000, 0);
    expect(s.meanSpeed).toBeCloseTo(100_000 / 60, 0);
    expect(s.peakSpeed).toBeCloseTo(100_000 / 60, 0);
  });

  it('classifies local mid-speed movement', () => {
    // 10_000 in 60s → ~167 u/s: above V_idle 50, below V_travel 800
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's1', x: 10_000, y: 0 }),
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 5 * 60_000 });
    expect(s.dominantClass).toBe('local');
  });

  it('does not connect path across segments (gap)', () => {
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's2', x: 100_000, y: 0 }),
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 5 * 60_000 });
    expect(s.pathLength).toBe(0);
    expect(s.gapMs).toBe(60_000);
    expect(s.observedActiveMs).toBe(0);
  });

  it('treats Δt above T_gap as gap and caps active contribution', () => {
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:10:00.000Z', segment_id: 's1', x: 100, y: 0 }), // 10 min > 5 min gap
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 20 * 60_000 });
    expect(s.gapMs).toBe(10 * 60_000);
    expect(s.observedActiveMs).toBe(0);
    expect(s.pathLength).toBe(0);
  });

  it('computes radius from centroid and density when active', () => {
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's1', x: 3000, y: 0 }),
      pt({ observed_at: '2026-07-14T10:02:00.000Z', segment_id: 's1', x: 0, y: 0 }),
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 10 * 60_000 });
    expect(s.sampleCount).toBe(3);
    expect(s.radius).toBeGreaterThan(0);
    expect(s.sampleDensityPerHour).toBeGreaterThan(0);
    expect(s.segmentCount).toBe(1);
  });

  it('breaks dominant-class ties traveling > local > stationary', () => {
    // Construct equal classMs via two edges of equal dt with forced classes is hard;
    // unit-test internal tie-break helper if exported, or equal ms via synthetic options.
    // Prefer: export pickDominantClass for test.
    const { pickDominantClass } = await import('./behaviorMetrics'); // use static import at top
  });
});
```

Fix the last test to use static import:

```ts
import { pickDominantClass, summarizeBehavior } from './behaviorMetrics';

it('breaks dominant-class ties traveling > local > stationary', () => {
  expect(pickDominantClass({ stationary: 10, local: 10, traveling: 10 })).toBe('traveling');
  expect(pickDominantClass({ stationary: 10, local: 10, traveling: 0 })).toBe('local');
  expect(pickDominantClass({ stationary: 0, local: 0, traveling: 0 })).toBe('unknown');
});
```

- [ ] **Step 2: Run tests — FAIL**

- [ ] **Step 3: Implement `behaviorMetrics.ts`**

```ts
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

  const sorted = [...samples].filter(finitePoint).sort(
    (a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at),
  );

  const windowStartMs = options.windowStartMs ?? (sorted[0] ? Date.parse(sorted[0].observed_at) : 0);
  const windowEndMs = options.windowEndMs ?? (sorted.length ? Date.parse(sorted[sorted.length - 1]!.observed_at) : 0);
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
      edges.push({ fromIndex: i, toIndex: i + 1, class: edgeClass, dist, dtMs, speed: edgeClass === 'gap' ? 0 : speed });
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
  };
}
```

- [ ] **Step 4: Run tests — PASS** (adjust fixtures if floating noise)

- [ ] **Step 5: Commit**

```bash
git add webui/src/behavior/behaviorMetrics.ts webui/src/behavior/behaviorMetrics.test.ts
git commit -m "feat(webui): implement trajectory behavior summarizeBehavior"
```

---

### Task 3: Format helpers

**Files:**
- Create: `webui/src/behavior/behaviorFormat.ts`
- Create: `webui/src/behavior/behaviorFormat.test.ts`

- [ ] **Step 1: Failing tests**

```ts
import { describe, expect, it } from 'vitest';
import {
  BEHAVIOR_CLASS_LABELS,
  formatBehaviorDistance,
  formatBehaviorShare,
  formatBehaviorSpeed,
  formatDominantLabel,
} from './behaviorFormat';

describe('behaviorFormat', () => {
  it('maps class labels in zh', () => {
    expect(BEHAVIOR_CLASS_LABELS.traveling).toBe('跑图');
    expect(BEHAVIOR_CLASS_LABELS.local).toBe('局部');
    expect(BEHAVIOR_CLASS_LABELS.stationary).toBe('挂机');
  });

  it('formats share as percent', () => {
    expect(formatBehaviorShare(0.333)).toMatch(/33%/);
    expect(formatBehaviorShare(0)).toBe('0%');
  });

  it('formats distance and speed with units', () => {
    expect(formatBehaviorDistance(1234.6)).toContain('世界坐标');
    expect(formatBehaviorSpeed(12.34)).toContain('坐标/秒');
  });

  it('formats dominant class', () => {
    expect(formatDominantLabel('traveling')).toBe('跑图');
    expect(formatDominantLabel('unknown')).toBe('未知');
  });
});
```

- [ ] **Step 2: FAIL then implement**

```ts
// webui/src/behavior/behaviorFormat.ts
import type { BehaviorDominantClass } from './behaviorTypes';

export const BEHAVIOR_CLASS_LABELS = {
  traveling: '跑图',
  local: '局部',
  stationary: '挂机',
} as const;

export function formatBehaviorShare(share: number): string {
  if (!Number.isFinite(share) || share <= 0) return '0%';
  return `${Math.round(share * 100)}%`;
}

export function formatBehaviorDistance(value: number): string {
  if (!Number.isFinite(value)) return '-';
  return `${Math.round(value).toLocaleString('zh-CN')} 世界坐标`;
}

export function formatBehaviorSpeed(value: number): string {
  if (!Number.isFinite(value)) return '-';
  const rounded = value >= 100 ? Math.round(value) : Math.round(value * 10) / 10;
  return `${rounded.toLocaleString('zh-CN')} 坐标/秒`;
}

export function formatDominantLabel(value: BehaviorDominantClass): string {
  if (value === 'unknown') return '未知';
  return BEHAVIOR_CLASS_LABELS[value];
}

export function formatDensityPerHour(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 点/时';
  const rounded = value >= 10 ? Math.round(value) : Math.round(value * 10) / 10;
  return `${rounded} 点/时`;
}
```

Duration: import `formatDuration` from `../utils` inside the panel (not required in format module).

- [ ] **Step 3: PASS + commit**

```bash
git add webui/src/behavior/behaviorFormat.ts webui/src/behavior/behaviorFormat.test.ts
git commit -m "feat(webui): add behavior summary format helpers"
```

---

### Task 4: `BehaviorSummaryPanel` UI

**Files:**
- Create: `webui/src/components/BehaviorSummaryPanel.tsx`
- Create: `webui/src/components/BehaviorSummaryPanel.test.tsx`
- Modify: `webui/src/styles.css`

- [ ] **Step 1: Component tests (failing)**

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { BehaviorSummaryPanel } from './BehaviorSummaryPanel';
import type { BehaviorSummary } from '../behavior/behaviorTypes';

function summary(partial: Partial<BehaviorSummary> = {}): BehaviorSummary {
  return {
    sampleCount: 10,
    segmentCount: 1,
    windowMs: 3600_000,
    observedActiveMs: 1800_000,
    pathLength: 50_000,
    radius: 12_000,
    meanSpeed: 40,
    peakSpeed: 900,
    sampleDensityPerHour: 20,
    classMs: { stationary: 600_000, local: 600_000, traveling: 600_000 },
    classShare: { stationary: 1 / 3, local: 1 / 3, traveling: 1 / 3 },
    gapMs: 0,
    gapShareOfWindow: 0,
    dominantClass: 'traveling',
    edges: [],
    ...partial,
  };
}

describe('BehaviorSummaryPanel', () => {
  it('renders empty when no samples and not loading', () => {
    render(<BehaviorSummaryPanel loading={false} selected summary={summary({ sampleCount: 0 })} />);
    expect(screen.getByText(/当前范围无位置样本/)).toBeInTheDocument();
  });

  it('renders mix labels and metrics when summary has samples', () => {
    render(<BehaviorSummaryPanel loading={false} selected summary={summary()} />);
    expect(screen.getByRole('region', { name: /行为摘要/ })).toBeInTheDocument();
    expect(screen.getByText('跑图')).toBeInTheDocument();
    expect(screen.getByText('局部')).toBeInTheDocument();
    expect(screen.getByText('挂机')).toBeInTheDocument();
    expect(screen.getByText(/观测活跃/)).toBeInTheDocument();
    expect(screen.getByText(/基于已加载的位置样本/)).toBeInTheDocument();
  });

  it('shows gap notice when gap share is high', () => {
    render(
      <BehaviorSummaryPanel
        loading={false}
        selected
        summary={summary({ gapShareOfWindow: 0.2, gapMs: 720_000 })}
      />,
    );
    expect(screen.getByText(/观测断档/)).toBeInTheDocument();
  });

  it('hides body when no player selected', () => {
    render(<BehaviorSummaryPanel loading={false} selected={false} summary={null} />);
    expect(screen.queryByRole('region', { name: /行为摘要/ })).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Implement panel**

```tsx
// webui/src/components/BehaviorSummaryPanel.tsx
import { Activity } from 'lucide-react';
import { GAP_SHARE_WARN, type BehaviorSummary } from '../behavior/behaviorTypes';
import {
  BEHAVIOR_CLASS_LABELS,
  formatBehaviorDistance,
  formatBehaviorShare,
  formatBehaviorSpeed,
  formatDensityPerHour,
  formatDominantLabel,
} from '../behavior/behaviorFormat';
import { formatDuration } from '../utils';

export type BehaviorSummaryPanelProps = {
  summary: BehaviorSummary | null;
  loading: boolean;
  selected: boolean;
};

export function BehaviorSummaryPanel({ summary, loading, selected }: BehaviorSummaryPanelProps) {
  if (!selected) return null;

  return (
    <section className="behavior-summary" aria-label="行为摘要">
      <header className="behavior-summary-header">
        <div>
          <p className="eyebrow">轨迹分析</p>
          <h3>行为摘要</h3>
          <p className="behavior-summary-note">基于已加载的位置样本 · 与政策在线时长不同</p>
        </div>
        <span className="behavior-summary-badge">
          <Activity size={15} />
          {summary && summary.sampleCount > 0 ? formatDominantLabel(summary.dominantClass) : '—'}
        </span>
      </header>

      {loading ? <p className="behavior-summary-empty">正在分析轨迹…</p> : null}

      {!loading && (!summary || summary.sampleCount === 0) ? (
        <p className="behavior-summary-empty">当前范围无位置样本，无法估计跑图/挂机行为。</p>
      ) : null}

      {!loading && summary && summary.sampleCount > 0 ? (
        <>
          <div className="behavior-mix" role="img" aria-label="行为占比">
            {(['traveling', 'local', 'stationary'] as const).map((key) => (
              <div className={`behavior-mix-seg behavior-mix-seg--${key}`} key={key} style={{ flexGrow: Math.max(summary.classShare[key], 0.02) }}>
                <span>{BEHAVIOR_CLASS_LABELS[key]} {formatBehaviorShare(summary.classShare[key])}</span>
              </div>
            ))}
          </div>
          <dl className="behavior-metrics">
            <div><dt>观测活跃</dt><dd>{formatDuration(summary.observedActiveMs)}</dd></div>
            <div><dt>活动半径</dt><dd>{formatBehaviorDistance(summary.radius)}</dd></div>
            <div><dt>路径长度</dt><dd>{formatBehaviorDistance(summary.pathLength)}</dd></div>
            <div><dt>均速</dt><dd>{formatBehaviorSpeed(summary.meanSpeed)}</dd></div>
            <div><dt>峰值速度</dt><dd>{formatBehaviorSpeed(summary.peakSpeed)}</dd></div>
            <div><dt>采样密度</dt><dd>{formatDensityPerHour(summary.sampleDensityPerHour)}</dd></div>
            <div><dt>位置点数</dt><dd>{summary.sampleCount}</dd></div>
            <div><dt>轨迹段</dt><dd>{summary.segmentCount}</dd></div>
          </dl>
          {summary.gapShareOfWindow > GAP_SHARE_WARN ? (
            <p className="behavior-summary-gap" role="status">
              存在观测断档，活跃时长未覆盖全部日历时间。
            </p>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
```

- [ ] **Step 3: CSS** (append to `styles.css`)

```css
.behavior-summary {
  display: grid;
  gap: 0.75rem;
  margin: 0.75rem 0 1rem;
  padding: 0.85rem 0.95rem;
  border: 1px solid #c5d0ca;
  border-radius: 0.45rem;
  background: #f7fbf9;
  box-shadow: 0 0.35rem 1rem rgba(41, 51, 45, 0.05);
}
.behavior-summary-header {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.6rem;
}
.behavior-summary-header h3 { margin: 0; font-size: 1rem; color: #14241f; }
.behavior-summary-note { margin: 0.2rem 0 0; color: #5b6963; font-size: 0.8rem; font-weight: 600; }
.behavior-summary-badge {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  min-height: 2rem;
  padding: 0 0.55rem;
  border: 1px solid #b7c7be;
  border-radius: 999px;
  background: #eef7f3;
  color: #255a50;
  font-size: 0.85rem;
  font-weight: 800;
}
.behavior-summary-empty { margin: 0; color: #5b6963; font-weight: 650; }
.behavior-mix {
  display: flex;
  overflow: hidden;
  min-height: 2rem;
  border-radius: 0.35rem;
  border: 1px solid #c5d0ca;
}
.behavior-mix-seg {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 0;
  padding: 0.25rem 0.35rem;
  color: #fffdf7;
  font-size: 0.72rem;
  font-weight: 800;
  white-space: nowrap;
}
.behavior-mix-seg span { overflow: hidden; text-overflow: ellipsis; }
.behavior-mix-seg--traveling { background: #8b5cf6; }
.behavior-mix-seg--local { background: #14c4d8; }
.behavior-mix-seg--stationary { background: #8a9a93; }
.behavior-metrics {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(9.5rem, 1fr));
  gap: 0.55rem 0.75rem;
  margin: 0;
}
.behavior-metrics div {
  display: grid;
  gap: 0.15rem;
  padding: 0.45rem 0.55rem;
  border: 1px solid #d5e0d9;
  border-radius: 0.35rem;
  background: #fffef9;
}
.behavior-metrics dt { margin: 0; color: #5b6963; font-size: 0.75rem; font-weight: 700; }
.behavior-metrics dd { margin: 0; color: #14241f; font-size: 0.9rem; font-weight: 800; }
.behavior-summary-gap {
  margin: 0;
  padding: 0.5rem 0.65rem;
  border: 1px solid #e0c48a;
  border-radius: 0.35rem;
  background: #fff8e8;
  color: #6a4210;
  font-size: 0.82rem;
  font-weight: 700;
}
```

- [ ] **Step 4: Tests PASS + commit**

```bash
git add webui/src/components/BehaviorSummaryPanel.tsx webui/src/components/BehaviorSummaryPanel.test.tsx webui/src/styles.css
git commit -m "feat(webui): add BehaviorSummaryPanel for trajectory analysis"
```

---

### Task 5: Wire `PlayerTimeline`

**Files:**
- Modify: `webui/src/components/PlayerTimeline.tsx`
- Optionally extend: `webui/src/components/PlayerTimeline.test.tsx` (one smoke if easy)

- [ ] **Step 1: Imports + memo**

Near other imports:

```ts
import { summarizeBehavior } from '../behavior/behaviorMetrics';
import { BehaviorSummaryPanel } from './BehaviorSummaryPanel';
```

After `items` / samples are available (same place `trajectorySamples` is already used or add):

```ts
const trajectoryPoints = useMemo(() => trajectorySamples(items), [items]);
const windowStartMs = useMemo(() => {
  const t = Date.parse(parsedStart?.toISOString?.() ?? start);
  // Prefer already-parsed start/end from existing validation path in the component.
  ...
}, [...]);
```

**Concrete wiring** (adapt to actual variable names in file):

There are already `parsedStart` / `parsedEnd` or equivalent ISO bounds used for fetch. Use those:

```ts
const behaviorSummary = useMemo(() => {
  if (!selectedID) return null;
  const startMs = /* Date.parse of selected range start */;
  const endMs = /* Date.parse of selected range end */;
  return summarizeBehavior(trajectorySamples(items), {
    windowStartMs: startMs,
    windowEndMs: endMs,
    includeEdges: true,
  });
}, [selectedID, items, start, end]);
```

If `start`/`end` are `datetime-local` strings, parse the same way the fetch effect does (`localInputValue` helpers already in file).

- [ ] **Step 2: Render panel under TimelineMap**

```tsx
<TimelineMap ... />
<BehaviorSummaryPanel
  selected={Boolean(selectedID)}
  loading={state.kind === 'loading'}
  summary={behaviorSummary}
/>
```

- [ ] **Step 3: Run full suite**

```bash
cd webui && npm test && npm run build
```

Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add webui/src/components/PlayerTimeline.tsx webui/src/components/PlayerTimeline.test.tsx
git commit -m "feat(webui): wire behavior summary into player timeline"
```

---

### Task 6: Final verification

- [ ] **Step 1:** `cd webui && npm test && npm run build` — green  
- [ ] **Step 2:** Spec acceptance checklist  
  1. Panel under map when player selected  
  2. Metrics match fixtures  
  3. Cross-segment / long gap no path  
  4. Empty / loading / gap warning  
  5. No new backend API  
- [ ] **Step 3:** Working tree clean for intentional files  

---

## Spec coverage

| Spec item | Task |
|-----------|------|
| Types + thresholds | Task 1 |
| summarizeBehavior algorithm | Task 2 |
| zh format + units | Task 3 |
| BehaviorSummaryPanel UI | Task 4 |
| PlayerTimeline wire | Task 5 |
| Tests / build | Task 2–6 |
| Phase C/B | Out of plan (hooks only: `edges` included) |

## Placeholder scan

No TBD. `PlayerTimeline` window parse must use existing start/end parsing already in that file (do not invent a second timezone path).
