import { useEffect, useRef, useState } from 'react';

/** Statistics tick in wall time; map playback remains at animation-frame cadence. */
export function useJourneyCursor(time: number, playing: boolean, key: string) {
  const latest = useRef(time);
  latest.current = time;
  const [snapshot, setSnapshot] = useState({ key, time });
  useEffect(() => {
    if (!playing) return;
    setSnapshot({ key, time: latest.current });
    const timer = window.setInterval(() => setSnapshot({ key, time: latest.current }), 1000);
    return () => window.clearInterval(timer);
  }, [playing, key]);
  // Seeking pauses the clock. A rewind must never leave future evidence visible.
  return !playing || snapshot.key !== key ? time : Math.min(time, snapshot.time);
}
