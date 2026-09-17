import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

export const HISTORY_SECONDS_PER_SECOND = 60;
export const WORKSPACE_SPEEDS = [1, 2, 4, 8] as const;
export type WorkspaceSpeed = typeof WORKSPACE_SPEEDS[number];

export function usePlaybackClock(start: number, end: number, resetKey: string, buffer: { bufferedUntil?: number; buffering?: boolean } = {}) {
  const [time, setTime] = useState(start);
  const timeRef = useRef(start);
  const previousKey = useRef(resetKey);
  const bufferedUntil = buffer.bufferedUntil ?? end;
  const buffering = buffer.buffering ?? false;
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState<WorkspaceSpeed>(1);
  // Reset before the newly enabled scrubber can accept input; a passive effect
  // could otherwise overwrite a seek made immediately after history resolves.
  useLayoutEffect(() => {
    if (previousKey.current !== resetKey) {
      previousKey.current = resetKey; timeRef.current = start; setPlaying(false);
    } else timeRef.current = Math.max(start, Math.min(end, timeRef.current));
    setTime(timeRef.current);
  }, [start, end, resetKey]);
  useEffect(() => {
    if (!playing || end <= start || buffering) return;
    if (timeRef.current >= end) { timeRef.current = start; setTime(start); }
    const availableEnd = Math.min(end, bufferedUntil);
    if (timeRef.current >= availableEnd) return;
    let frame = 0;
    let last = performance.now();
    const tick = (now: number) => {
      // Do not fast-forward through a suspended tab or a stalled browser.
      const delta = Math.max(0, Math.min(100, now - last));
      last = now;
      timeRef.current = Math.min(availableEnd, timeRef.current + delta * HISTORY_SECONDS_PER_SECOND * speed);
      setTime(timeRef.current);
      if (timeRef.current >= end) setPlaying(false);
      else if (timeRef.current < availableEnd) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [playing, speed, start, end, buffering, bufferedUntil]);
  useEffect(() => {
    const visibility = () => { if (document.visibilityState === 'hidden') setPlaying(false); };
    document.addEventListener('visibilitychange', visibility);
    return () => document.removeEventListener('visibilitychange', visibility);
  }, []);
  const seek = useCallback((next: number) => {
    if (!Number.isFinite(next)) return;
    setPlaying(false);
    timeRef.current = Math.max(start, Math.min(end, next));
    setTime(timeRef.current);
  }, [start, end]);
  // Bounds can change as a request resolves; never paint the previous window's time.
  return { time: Math.max(start, Math.min(end, time)), playing, setPlaying, speed, setSpeed, seek };
}
