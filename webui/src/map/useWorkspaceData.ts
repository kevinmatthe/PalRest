import { useEffect, useRef, useState } from 'react';
import { getLivePositions, getPlayerTimeline, type LivePositionsResponse, type PlayerTimelineResponse } from '../api';

export function useLivePositions(refreshKey: number, active = true) {
  const [data, setData] = useState<LivePositionsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [now, setNow] = useState(Date.now);
  const [visible, setVisible] = useState(() => document.visibilityState !== 'hidden');
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    setVisible(document.visibilityState !== 'hidden');
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    const visibility = () => setVisible(document.visibilityState !== 'hidden');
    document.addEventListener('visibilitychange', visibility);
    return () => { window.clearInterval(timer); document.removeEventListener('visibilitychange', visibility); };
  }, [active]);
  useEffect(() => {
    if (!visible || !active) return;
    let cancelled = false;
    let timer = 0;
    let timeout = 0;
    let controller: AbortController | undefined;
    async function poll() {
      controller = new AbortController();
      timeout = window.setTimeout(() => controller?.abort(), 10000);
      try {
        const next = await getLivePositions(controller.signal);
        if (cancelled) return;
        setData(current => {
          const currentTime = Date.parse(current?.as_of ?? '');
          const nextTime = Date.parse(next.as_of ?? '');
          if (Number.isFinite(currentTime) && (!Number.isFinite(nextTime) || nextTime < currentTime)) return current;
          return next;
        });
        setError(null);
      } catch (err) {
        if (!cancelled) setError(controller.signal.aborted ? '位置请求超时，保留最后观测' : err instanceof Error ? err.message : '位置暂时不可用');
      } finally {
        window.clearTimeout(timeout);
        if (!cancelled) timer = window.setTimeout(() => void poll(), 1000);
      }
    }
    void poll();
    return () => { cancelled = true; controller?.abort(); window.clearTimeout(timer); window.clearTimeout(timeout); };
  }, [refreshKey, visible, active]);
  const ageMs = data?.as_of ? Math.max(0, now - Date.parse(data.as_of)) : Number.POSITIVE_INFINITY;
  return { data, error, ageMs, stale: Boolean(error) || !Number.isFinite(ageMs) || ageMs > 60000 };
}

type HistoryState = { key: string; data?: PlayerTimelineResponse; loading: boolean; error?: string };
export function useHistoryWindow(active: boolean, userID: string, start: number, end: number, revision = 0) {
  const key = `${userID}:${start}:${end}:${revision}`;
  const [state, setState] = useState<HistoryState>({ key: '', loading: false });
  const cached = useRef<HistoryState | null>(null);
  useEffect(() => {
    if (!active || !userID) return;
    if (cached.current?.key === key && cached.current.data) { setState(cached.current); return; }
    const controller = new AbortController();
    setState({ key, loading: true });
    const timeout = window.setTimeout(() => controller.abort(), 20000);
    let cancelled = false;
    void getPlayerTimeline(userID, new Date(start).toISOString(), new Date(end).toISOString(), 500, controller.signal)
      .then(data => {
        if (cancelled || controller.signal.aborted) return;
        const next = { key, data, loading: false };
        cached.current = next;
        setState(next);
      })
      .catch(err => {
        if (!cancelled) setState({ key, loading: false, error: controller.signal.aborted ? '历史请求超时，请重试' : err instanceof Error ? err.message : '历史记录暂时不可用' });
      })
      .finally(() => window.clearTimeout(timeout));
    return () => { cancelled = true; controller.abort(); window.clearTimeout(timeout); };
  }, [active, userID, start, end, key]);
  return state.key === key ? state : { key, loading: active && Boolean(userID) };
}
