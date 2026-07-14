export type PlaySpeed = 1 | 2 | 4;
export const BASE_PLAY_INTERVAL_MS = 800;
export const PLAY_SPEEDS: PlaySpeed[] = [1, 2, 4];

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
