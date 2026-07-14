/** Estimated row height for windowing (matches ~dense timeline-entry + padding). */
export const DEFAULT_ROW_ESTIMATE_PX = 148;
export const DEFAULT_OVERSCAN = 6;

export type VirtualWindow = {
  start: number;
  end: number;
  offsetTop: number;
  totalHeight: number;
};

/**
 * Compute a window of row indices to render for a scrollable list of fixed estimate height.
 * end is exclusive.
 */
export function virtualWindow(
  count: number,
  scrollTop: number,
  viewportHeight: number,
  estimatePx = DEFAULT_ROW_ESTIMATE_PX,
  overscan = DEFAULT_OVERSCAN,
): VirtualWindow {
  const safeCount = Math.max(0, count);
  const estimate = Math.max(1, estimatePx);
  const totalHeight = safeCount * estimate;
  if (safeCount === 0) return { start: 0, end: 0, offsetTop: 0, totalHeight: 0 };

  const rawStart = Math.floor(Math.max(0, scrollTop) / estimate) - overscan;
  const start = Math.max(0, rawStart);
  const visible = Math.ceil(Math.max(1, viewportHeight) / estimate) + 1;
  const end = Math.min(safeCount, start + visible + overscan * 2);
  return {
    start,
    end,
    offsetTop: start * estimate,
    totalHeight,
  };
}

/** ScrollTop that centers (approximately) the active row in the viewport. */
export function scrollTopForIndex(
  index: number,
  viewportHeight: number,
  estimatePx = DEFAULT_ROW_ESTIMATE_PX,
): number {
  const estimate = Math.max(1, estimatePx);
  const i = Math.max(0, index);
  return Math.max(0, i * estimate - Math.max(0, viewportHeight / 2 - estimate / 2));
}
