export type PlaySpeed = 1 | 2 | 4;
export type PlayMode = 'index' | 'time';

export const BASE_PLAY_INTERVAL_MS = 800;
export const PLAY_SPEEDS: PlaySpeed[] = [1, 2, 4];
/** Real elapsed time is compressed by this factor for time-proportional play (1×). */
export const TIME_PLAY_COMPRESS = 450;
export const TIME_PLAY_MIN_MS = 50;
export const TIME_PLAY_MAX_MS = 5_000;

export function playIntervalMs(speed: PlaySpeed): number {
  return BASE_PLAY_INTERVAL_MS / speed;
}

/** Advance one step; done=true means pause and stay on last index. */
export function nextCursorIndex(current: number, length: number): { index: number; done: boolean } {
  if (length <= 1) return { index: 0, done: true };
  if (current >= length - 1) return { index: length - 1, done: true };
  const index = current + 1;
  return { index, done: index >= length - 1 };
}

/** Step backward; done=true when already at the first index. */
export function prevCursorIndex(current: number, length: number): { index: number; done: boolean } {
  if (length <= 1) return { index: 0, done: true };
  if (current <= 0) return { index: 0, done: true };
  return { index: current - 1, done: false };
}

/**
 * Delay before advancing from currentAt to nextAt.
 * - index mode: fixed interval scaled by speed
 * - time mode: real Δt / TIME_PLAY_COMPRESS / speed, clamped
 */
export function playStepDelayMs(
  mode: PlayMode,
  speed: PlaySpeed,
  currentAt: string | undefined,
  nextAt: string | undefined,
): number {
  if (mode === 'index') return playIntervalMs(speed);
  const start = currentAt ? Date.parse(currentAt) : Number.NaN;
  const end = nextAt ? Date.parse(nextAt) : Number.NaN;
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) {
    return playIntervalMs(speed);
  }
  const wait = (end - start) / TIME_PLAY_COMPRESS / speed;
  return Math.min(TIME_PLAY_MAX_MS, Math.max(TIME_PLAY_MIN_MS, wait));
}
