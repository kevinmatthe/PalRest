/** Sliding observation window helpers (fixed duration, pan as a whole). */

export const HOUR_MS = 3_600_000;
export const DAY_MS = 24 * HOUR_MS;
/** How far back the scrubber can slide (also max window span). */
export const DEFAULT_HORIZON_MS = 31 * DAY_MS;

export type WindowPreset = {
  id: string;
  label: string;
  /** Short chip label */
  shortLabel: string;
  ms: number;
};

export const WINDOW_PRESETS: WindowPreset[] = [
  { id: '1h', label: '最近 1 小时', shortLabel: '1 小时', ms: HOUR_MS },
  { id: '6h', label: '最近 6 小时', shortLabel: '6 小时', ms: 6 * HOUR_MS },
  { id: '24h', label: '最近 24 小时', shortLabel: '24 小时', ms: DAY_MS },
  { id: '7d', label: '最近 7 天', shortLabel: '7 天', ms: 7 * DAY_MS },
];

export function clampWindowMs(windowMs: number, horizonMs = DEFAULT_HORIZON_MS): number {
  if (!Number.isFinite(windowMs) || windowMs <= 0) return HOUR_MS;
  return Math.min(horizonMs, Math.max(60_000, windowMs));
}

/** Earliest allowed window end so start stays within [now - horizon, now]. */
export function minWindowEnd(windowMs: number, nowMs: number, horizonMs = DEFAULT_HORIZON_MS): number {
  const w = clampWindowMs(windowMs, horizonMs);
  return nowMs - horizonMs + w;
}

export function maxWindowEnd(nowMs: number): number {
  return nowMs;
}

export function clampWindowEnd(
  endMs: number,
  windowMs: number,
  nowMs: number,
  horizonMs = DEFAULT_HORIZON_MS,
): number {
  const w = clampWindowMs(windowMs, horizonMs);
  const minEnd = minWindowEnd(w, nowMs, horizonMs);
  const maxEnd = maxWindowEnd(nowMs);
  return Math.min(maxEnd, Math.max(minEnd, endMs));
}

export function windowFromEnd(
  endMs: number,
  windowMs: number,
  nowMs: number,
  horizonMs = DEFAULT_HORIZON_MS,
): { startMs: number; endMs: number; windowMs: number } {
  const w = clampWindowMs(windowMs, horizonMs);
  const end = clampWindowEnd(endMs, w, nowMs, horizonMs);
  return { startMs: end - w, endMs: end, windowMs: w };
}

/** 0 = oldest position, 1 = window ends at now. */
export function scrubberPosition(
  endMs: number,
  windowMs: number,
  nowMs: number,
  horizonMs = DEFAULT_HORIZON_MS,
): number {
  const w = clampWindowMs(windowMs, horizonMs);
  const minEnd = minWindowEnd(w, nowMs, horizonMs);
  const maxEnd = maxWindowEnd(nowMs);
  if (maxEnd <= minEnd) return 1;
  const t = (endMs - minEnd) / (maxEnd - minEnd);
  return Math.min(1, Math.max(0, t));
}

export function endFromScrubber(
  position: number,
  windowMs: number,
  nowMs: number,
  horizonMs = DEFAULT_HORIZON_MS,
): number {
  const w = clampWindowMs(windowMs, horizonMs);
  const minEnd = minWindowEnd(w, nowMs, horizonMs);
  const maxEnd = maxWindowEnd(nowMs);
  const t = Math.min(1, Math.max(0, position));
  return minEnd + t * (maxEnd - minEnd);
}

/** Left edge of the window on the horizon track, 0..1. */
export function windowTrackLeft(
  startMs: number,
  nowMs: number,
  horizonMs = DEFAULT_HORIZON_MS,
): number {
  const horizonStart = nowMs - horizonMs;
  if (horizonMs <= 0) return 0;
  return Math.min(1, Math.max(0, (startMs - horizonStart) / horizonMs));
}

export function windowTrackWidth(windowMs: number, horizonMs = DEFAULT_HORIZON_MS): number {
  const w = clampWindowMs(windowMs, horizonMs);
  if (horizonMs <= 0) return 1;
  return Math.min(1, Math.max(0.02, w / horizonMs));
}

/** Match preset if duration is within 90s (datetime-local minute rounding). */
export function matchPreset(windowMs: number, presets: WindowPreset[] = WINDOW_PRESETS): WindowPreset | null {
  for (const preset of presets) {
    if (Math.abs(windowMs - preset.ms) <= 90_000) return preset;
  }
  return null;
}

export function isPinnedToNow(endMs: number, nowMs: number, slackMs = 90_000): boolean {
  return nowMs - endMs <= slackMs;
}
