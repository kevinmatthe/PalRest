# Trajectory Behavior × Landmarks (A2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enrich phase-A behavior summary with landmark dwell, activity anchor, and suspected teleports using existing `MAP_LANDMARKS`.

**Architecture:** Pure `enrichBehaviorWithLandmarks` / `analyzeTrajectoryBehavior` in `webui/src/behavior/`; extend types + panel; compose after `summarizeBehavior` in `PlayerTimeline`.

**Tech Stack:** TypeScript, Vitest, React Testing Library.

**Spec:** `docs/superpowers/specs/2026-07-14-trajectory-behavior-landmarks-design.md`

---

## File Structure

| File | Role |
|------|------|
| Modify `webui/src/behavior/behaviorTypes.ts` | New types + summary fields + constants |
| Create `webui/src/behavior/behaviorLandmarks.ts` | Enrichment pure functions |
| Create `webui/src/behavior/behaviorLandmarks.test.ts` | Fixtures with tiny landmark tables |
| Modify `webui/src/behavior/behaviorFormat.ts` | Kind / teleport labels |
| Modify `webui/src/behavior/behaviorFormat.test.ts` | Format tests |
| Modify `webui/src/components/BehaviorSummaryPanel.tsx` | New sections |
| Modify `webui/src/components/BehaviorSummaryPanel.test.tsx` | UI coverage |
| Modify `webui/src/components/PlayerTimeline.tsx` | Call enrich / wrapper |
| Modify `webui/src/styles.css` | List styles if needed |

---

### Task 1: Types + constants for landmarks enrichment

**Files:**
- Modify: `webui/src/behavior/behaviorTypes.ts`

- [ ] **Step 1: Add types and constants**

```ts
export const TELEPORT_MIN_DIST = 50_000;
export const LANDMARK_DWELL_TOP_N = 5;
export const TELEPORT_TOP_N = 5;

export type LandmarkDwell = {
  landmarkId: string;
  nameZh: string;
  kind: 'fast_travel' | 'boss_tower';
  dwellMs: number;
  sampleHits: number;
};

export type TeleportSuspect = {
  fromLandmarkId?: string;
  fromNameZh?: string;
  toLandmarkId?: string;
  toNameZh?: string;
  dist: number;
  dtMs: number;
  reason: 'gap_hop' | 'long_jump';
  at: string;
};

// Extend BehaviorSummary:
  landmarkDwells: LandmarkDwell[];
  activityAnchor?: LandmarkDwell;
  teleportSuspects: TeleportSuspect[];
  landmarkHitRate: number;
```

Update `summarizeBehavior` return in `behaviorMetrics.ts` to include empty defaults:

```ts
landmarkDwells: [],
teleportSuspects: [],
landmarkHitRate: 0,
// activityAnchor omitted
```

- [ ] **Step 2: Fix any broken tests** that construct full `BehaviorSummary` (panel tests, metrics if any)

- [ ] **Step 3: Commit**

```bash
git add webui/src/behavior/behaviorTypes.ts webui/src/behavior/behaviorMetrics.ts webui/src/components/BehaviorSummaryPanel.test.tsx
git commit -m "feat(webui): extend BehaviorSummary with landmark fields"
```

---

### Task 2: `enrichBehaviorWithLandmarks` (TDD)

**Files:**
- Create: `webui/src/behavior/behaviorLandmarks.ts`
- Create: `webui/src/behavior/behaviorLandmarks.test.ts`

- [ ] **Step 1: Failing tests** with local landmark fixtures (not full MAP_LANDMARKS):

```ts
const landmarks = [
  { id: 'ft-a', nameZh: '甲地', x: 0, y: 0, kind: 'fast_travel' as const },
  { id: 'ft-b', nameZh: '乙地', x: 100_000, y: 0, kind: 'fast_travel' as const },
];
```

Cases:
1. Two stationary samples near ft-a → dwell on 甲地, anchor 甲地  
2. Samples far away → empty dwells, hitRate 0  
3. Gap hop 0→100000 across segments with FT hits → teleport_suspect gap_hop  
4. Same segment 60s + 100000 dist both near FTs → long_jump  
5. Top-N ordering by dwellMs  

- [ ] **Step 2: Implement**

```ts
export function enrichBehaviorWithLandmarks(
  summary: BehaviorSummary,
  samples: BehaviorPoint[],
  options?: {
    landmarks?: MapLandmark[];
    radius?: number;
    teleportMinDist?: number;
    tGapMs?: number;
    tActiveCapMs?: number;
  },
): BehaviorSummary
```

Algorithm per design. Use `nearestLandmark` from `../map/mapLandmarks`.

Also export convenience:

```ts
export function analyzeTrajectoryBehavior(
  samples: BehaviorPoint[],
  options?: SummarizeBehaviorOptions & { landmarks?: MapLandmark[]; landmarkRadius?: number },
): BehaviorSummary {
  const base = summarizeBehavior(samples, options);
  return enrichBehaviorWithLandmarks(base, samples, {
    landmarks: options?.landmarks,
    radius: options?.landmarkRadius,
    tGapMs: options?.tGapMs,
    tActiveCapMs: options?.tActiveCapMs,
  });
}
```

Note: edges indices refer to **sorted** samples inside summarizeBehavior—enrich must **re-sort the same way** before indexing. Prefer exporting `sortBehaviorSamples` from metrics or re-sort identically in enrich.

**Critical:** Align index space with edges. Implementation approach:
1. Re-sort samples same as `summarizeBehavior` (copy sort helper to shared or export from metrics).
2. Build hits[] for sorted array.
3. Walk edges if present; else recompute pairs.

- [ ] **Step 3: Green tests + commit**

```bash
git add webui/src/behavior/behaviorLandmarks.ts webui/src/behavior/behaviorLandmarks.test.ts webui/src/behavior/behaviorMetrics.ts
git commit -m "feat(webui): enrich behavior summary with landmark dwell and teleports"
```

---

### Task 3: Format + panel UI

**Files:**
- Modify format + panel + styles + tests

- [ ] **Step 1: Format helpers**

```ts
export function formatLandmarkKind(kind: 'fast_travel' | 'boss_tower'): string {
  return kind === 'boss_tower' ? '首领塔' : '传送点';
}
export function formatTeleportReason(reason: 'gap_hop' | 'long_jump'): string {
  return reason === 'gap_hop' ? '跨段' : '大跳';
}
export function formatTeleportLine(t: TeleportSuspect): string {
  const from = t.fromNameZh ?? '野外';
  const to = t.toNameZh ?? '野外';
  return `${from} → ${to}`;
}
```

- [ ] **Step 2: Panel sections** after metrics grid

- 活动锚点  
- 驻留 Top (ol)  
- 疑似传送 (ol)  

- [ ] **Step 3: Styles** for `.behavior-landmark-list` etc.

- [ ] **Step 4: Tests + commit**

```bash
git commit -m "feat(webui): show landmark dwell and teleports in behavior panel"
```

---

### Task 4: Wire PlayerTimeline

**Files:**
- Modify: `webui/src/components/PlayerTimeline.tsx`

Replace `summarizeBehavior(...)` with `analyzeTrajectoryBehavior(...)` from `behaviorLandmarks.ts` (defaults to full MAP_LANDMARKS).

```bash
cd webui && npm test && npm run build
git commit -m "feat(webui): analyze trajectory behavior with world landmarks"
```

---

### Task 5: Final verification

- Full test + build  
- Spec acceptance checklist  
- Manual: player near FT shows 驻留/锚点  

---

## Spec coverage

| Spec | Task |
|------|------|
| Types/constants | 1 |
| Enrichment algorithms | 2 |
| Panel UI | 3 |
| Timeline wire | 4 |
| Tests/build | 2–5 |
