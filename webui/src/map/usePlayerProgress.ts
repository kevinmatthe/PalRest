import { useEffect, useRef, useState } from 'react';
import { getPlayerProgress, type PlayerProgressResponse } from '../api';
type ProgressState = { key: string; data?: PlayerProgressResponse; loading: boolean; error?: string };
export function usePlayerProgress(active: boolean, userID: string, mode: 'live' | 'history', start: number, end: number, revision = 0) {
  const key = `${userID}:${mode}:${start}:${end}:${revision}`;
  const [state, setState] = useState<ProgressState>({ key: '', loading: false });
  const cache = useRef(new Map<string, ProgressState>());
  useEffect(() => {
    if (!active || !userID) return;
    const cached = cache.current.get(key);
    if (mode === 'history' && cached?.data) { setState(cached); return; }
    let cancelled = false;
    let timer = 0;
    let timeout = 0;
    let controller: AbortController;
    setState(cached ?? { key, loading: true });
    async function query() {
      controller = new AbortController();
      timeout = window.setTimeout(() => controller.abort(), 20000);
      const queryEnd = mode === 'live' ? Date.now() : end;
      const queryStart = mode === 'live' ? queryEnd - (end - start) : start;
      try {
        const data = await getPlayerProgress(userID, new Date(queryStart).toISOString(), new Date(queryEnd).toISOString(), 200, controller.signal);
        if (cancelled || controller.signal.aborted) return;
        const next = { key, data, loading: false };
        cache.current.set(key, next);
        if (cache.current.size > 12) cache.current.delete(cache.current.keys().next().value!);
        setState(next);
      } catch (err) {
        if (!cancelled) setState(current => ({ key, data: current.key === key ? current.data : undefined, loading: false,
          error: controller.signal.aborted ? '进度请求超时，请重试' : err instanceof Error ? err.message : '玩家进度暂时不可用' }));
      } finally {
        window.clearTimeout(timeout);
        if (!cancelled && mode === 'live') timer = window.setTimeout(() => void query(), 60000);
      }
    }
    void query();
    return () => { cancelled = true; controller?.abort(); window.clearTimeout(timer); window.clearTimeout(timeout); };
  }, [active, userID, mode, start, end, key]);
  return state.key === key ? state : { key, loading: active && Boolean(userID) };
}
