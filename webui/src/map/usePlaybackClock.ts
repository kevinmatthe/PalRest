import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

export const HISTORY_SECONDS_PER_SECOND = 60;
export const WORKSPACE_SPEEDS = [1, 2, 4, 8] as const;
export type WorkspaceSpeed = typeof WORKSPACE_SPEEDS[number];

export function usePlaybackClock(start: number, end: number, resetKey: string) {
  const [time, setTime] = useState(start);
  const timeRef = useRef(start);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState<WorkspaceSpeed>(1);
  // Reset before the newly enabled scrubber can accept input; a passive effect
  // could otherwise overwrite a seek made immediately after history resolves.
  useLayoutEffect(() => { timeRef.current = start; setTime(start); setPlaying(false); }, [start, end, resetKey]);
  useEffect(() => {
    if (!playing || end <= start) return;
    if (timeRef.current >= end) { timeRef.current = start; setTime(start); }
    let frame = 0;
    let last = performance.now();
    const tick = (now: number) => {
      // Do not fast-forward through a suspended tab or a stalled browser.
      const delta = Math.max(0, Math.min(100, now - last));
      last = now;
      timeRef.current = Math.min(end, timeRef.current + delta * HISTORY_SECONDS_PER_SECOND * speed);
      setTime(timeRef.current);
      if (timeRef.current >= end) setPlaying(false);
      else frame = requestAnimationFrame(tick);
    };
    const visibility = () => { if (document.visibilityState === 'hidden') setPlaying(false); };
    document.addEventListener('visibilitychange', visibility);
    frame = requestAnimationFrame(tick);
    return () => { cancelAnimationFrame(frame); document.removeEventListener('visibilitychange', visibility); };
  }, [playing, speed, start, end]);
  const seek = useCallback((next: number) => {
    if (!Number.isFinite(next)) return;
    setPlaying(false);
    timeRef.current = Math.max(start, Math.min(end, next));
    setTime(timeRef.current);
  }, [start, end]);
  // Bounds can change as a request resolves; never paint the previous window's time.
  return { time: Math.max(start, Math.min(end, time)), playing, setPlaying, speed, setSpeed, seek };
}
