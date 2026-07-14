export type TickKind = 'event' | 'trajectory' | 'private';

export type TimelineTickInput = {
  at: string;
  kind: TickKind;
  key: string;
};

export type MergedTimelineTick = {
  /** 0–100 position on the track */
  leftPercent: number;
  kind: TickKind;
  active: boolean;
  count: number;
  key: string;
};

const KIND_RANK: Record<TickKind, number> = {
  event: 3,
  private: 2,
  trajectory: 1,
};

export function timelinePercent(at: string, startMS: number, endMS: number): number {
  const current = Date.parse(at);
  if (!Number.isFinite(current) || endMS <= startMS) return 0;
  return Math.min(100, Math.max(0, ((current - startMS) / (endMS - startMS)) * 100));
}

/**
 * Collapse dense timeline markers into one tick per pixel column so a 500-item
 * range does not create 500 DOM nodes. Prefer event styling over private/trajectory
 * when multiple kinds share a column; mark active if any item in the column is active.
 */
export function mergeTimelineTicks(
  items: TimelineTickInput[],
  startMS: number,
  endMS: number,
  trackWidthPx: number,
  activeIndex: number,
): MergedTimelineTick[] {
  const width = Math.max(1, Math.floor(trackWidthPx));
  const buckets = new Map<number, MergedTimelineTick>();

  items.forEach((item, index) => {
    const pct = timelinePercent(item.at, startMS, endMS);
    const col = Math.min(width - 1, Math.max(0, Math.round((pct / 100) * (width - 1))));
    const active = index === activeIndex;
    const existing = buckets.get(col);
    if (!existing) {
      buckets.set(col, {
        leftPercent: (col / Math.max(1, width - 1)) * 100,
        kind: item.kind,
        active,
        count: 1,
        key: `tick-${col}`,
      });
      return;
    }
    existing.count += 1;
    existing.active = existing.active || active;
    if (KIND_RANK[item.kind] > KIND_RANK[existing.kind]) existing.kind = item.kind;
  });

  return [...buckets.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([, tick]) => tick);
}
