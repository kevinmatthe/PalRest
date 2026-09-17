import { useEffect, useState } from 'react';
import { getPlayerTimeline, type PlayerTimelineResponse } from '../api';

type State = { key: string; data?: PlayerTimelineResponse; loading: boolean; error?: string; start?: number; end?: number };

/** Live summary data is deliberately separate from the frozen historical window. */
export function useJourneyTimeline(active: boolean, userID: string, duration: number, revision = 0) {
  const key = `${userID}:${duration}:${revision}`;
  const valid = Boolean(userID) && Number.isFinite(duration) && duration > 0 && duration <= 31 * 86400000;
  const [state, setState] = useState<State>({ key: '', loading: false });
  const [visible, setVisible] = useState(() => document.visibilityState !== 'hidden');
  useEffect(() => {
    const update = () => setVisible(document.visibilityState !== 'hidden');
    update();
    document.addEventListener('visibilitychange', update);
    return () => document.removeEventListener('visibilitychange', update);
  }, []);
  useEffect(() => {
    if (!active || !visible || !valid) return;
    let cancelled = false;
    let timer = 0;
    let timeout = 0;
    let controller: AbortController;
    setState(current => current.key === key ? current : { key, loading: true });
    async function query() {
      controller = new AbortController();
      timeout = window.setTimeout(() => controller.abort(), 20000);
      const end = Date.now(), start = end - duration;
      try {
        const data = await getPlayerTimeline(userID, new Date(start).toISOString(), new Date(end).toISOString(), 500, controller.signal);
        if (cancelled || controller.signal.aborted) return;
        setState({ key, data, loading: false, start, end });
      } catch (err) {
        if (!cancelled) setState(current => ({ ...(current.key === key ? current : {}), key, loading: false,
          error: controller.signal.aborted ? '小结请求超时，请重试' : err instanceof Error ? err.message : '小结暂时不可用' }));
      } finally {
        window.clearTimeout(timeout);
        if (!cancelled) timer = window.setTimeout(() => void query(), 60000);
      }
    }
    void query();
    return () => { cancelled = true; controller?.abort(); window.clearTimeout(timer); window.clearTimeout(timeout); };
  }, [active, visible, valid, userID, duration, key]);
  return state.key === key ? state : { key, loading: active && visible && valid } as State;
}
