import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { useEffect, useState } from 'react';
import { usePlaybackClock } from './usePlaybackClock';
import { afterEach, expect, it, vi } from 'vitest';
import { getPlayerTimeline, type PlayerTimelineResponse, type TrajectorySample } from '../api';
import { useHistoryStream } from './useHistoryStream';
vi.mock('../api', () => ({ getPlayerTimeline: vi.fn() }));
const CHUNK = 600000, PAD = 330000, START = 3600000, END = START + 12 * CHUNK;
const iso = (n: number) => new Date(n).toISOString();
const row = (time: number, ref = String(time)): TrajectorySample => ({ user_id: 'u', segment_id: 's', observed_at: iso(time), x: 1, y: 2, ping: 0, level: 1, source_ref: ref, runtime_epoch: 1 });
const response = (trajectories: TrajectorySample[] = [], user_id = 'u'): PlayerTimelineResponse => ({ user_id, trajectories, events: [], private_samples: [], trajectory_total: trajectories.length, event_total: 0 });
const metadata = () => ({ ...response(), trajectory_total: 1000, range_start: iso(START), range_end: iso(END) });
function serveRows(rows: TrajectorySample[]) {
  vi.mocked(getPlayerTimeline).mockImplementation(async (_u, a, b, limit) => {
    if (limit === 1) return { ...metadata(), trajectory_total: rows.length };
    const matches = rows.filter(r => Date.parse(r.observed_at) >= Date.parse(a!) && Date.parse(r.observed_at) <= Date.parse(b!));
    return { ...response(matches.slice(-(limit ?? 500))), trajectory_total: matches.length };
  });
}
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r; }); return { promise, resolve }; }
afterEach(() => { cleanup(); vi.resetAllMocks(); vi.useRealTimers(); });

it('uses complete bootstrap responses (including old responses without totals) without extra queries', async () => {
  vi.mocked(getPlayerTimeline).mockResolvedValue({ user_id: 'u', trajectories: [row(START)], events: [], private_samples: [] });
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, END, null));
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(getPlayerTimeline).toHaveBeenCalledTimes(1);
  expect(vi.mocked(getPlayerTimeline).mock.calls[0].slice(0, 4)).toEqual(['u', iso(START), iso(END), 1]);
  expect(result.current.data?.trajectories).toHaveLength(1);
  expect(result.current.buffering).toBe(false);
  expect(result.current.rangeStart).toBe(START);
  expect(result.current.rangeEnd).toBe(START);
});

it('loads the target then the next padded absolute-epoch chunk serially', async () => {
  const first = deferred<PlayerTimelineResponse>();
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce(metadata()).mockReturnValueOnce(first.promise).mockResolvedValue(response());
  const { result } = renderHook(() => useHistoryStream(true, 'u', START + 123, END, START + 200));
  await waitFor(() => expect(getPlayerTimeline).toHaveBeenCalledTimes(2));
  expect(result.current.loading).toBe(false);
  expect(result.current.buffering).toBe(true);
  expect(vi.mocked(getPlayerTimeline).mock.calls[1].slice(1, 4)).toEqual([iso(START + 123), iso(START + CHUNK + PAD), 500]);
  await act(async () => first.resolve(response([row(START + 200)])));
  await waitFor(() => expect(getPlayerTimeline).toHaveBeenCalledTimes(3));
  expect(vi.mocked(getPlayerTimeline).mock.calls[2].slice(1, 4)).toEqual([iso(START + CHUNK - PAD), iso(START + 2 * CHUNK + PAD), 500]);
  expect(result.current.buffering).toBe(false);
  expect(result.current.loadedStart).toBe(START + 123);
  expect(result.current.loadedEnd).toBe(START + 2 * CHUNK + PAD);
});

it('recursively loads all dense rows beyond500, deduplicating overlaps and reporting buffered totals', async () => {
  const rows = Array.from({ length: 830 }, (_, i) => row(START + i * 1000));
  serveRows(rows);
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, END, START));
  await waitFor(() => expect(result.current.data?.trajectories).toHaveLength(830));
  expect(result.current.data?.trajectory_total).toBe(830);
  expect(result.current.data?.trajectories.map(r => r.source_ref)).toEqual(rows.map(r => r.source_ref));
  expect(getPlayerTimeline).toHaveBeenCalledTimes(9); // both overlapping chunks require subdivision
});

it('aborts an obsolete prefetch on a random seek and never exposes disconnected old chunks', async () => {
  const prefetch = deferred<PlayerTimelineResponse>();
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce(metadata()).mockResolvedValueOnce(response([row(START)]))
    .mockReturnValueOnce(prefetch.promise).mockResolvedValue(response([row(START + 8 * CHUNK)]));
  const { result, rerender } = renderHook(({ cursor }) => useHistoryStream(true, 'u', START, END, cursor), { initialProps: { cursor: START } });
  await waitFor(() => expect(getPlayerTimeline).toHaveBeenCalledTimes(3));
  const signal = vi.mocked(getPlayerTimeline).mock.calls[2][4];
  rerender({ cursor: START + 8 * CHUNK });
  expect(signal?.aborted).toBe(true);
  expect(result.current.buffering).toBe(true);
  expect(result.current.data).toBeUndefined();
  await act(async () => prefetch.resolve(response([row(START + CHUNK)])));
  await waitFor(() => expect(result.current.buffering).toBe(false));
  expect(result.current.data?.trajectories.map(r => r.observed_at)).toEqual([iso(START + 8 * CHUNK)]);
  expect(result.current.loadedStart).toBe(START + 8 * CHUNK - PAD);
});

it('evicts distant windows rather than retaining the whole replay', async () => {
  serveRows(Array.from({ length: 100 }, (_, i) => row(START + i * 60000)));
  const { result, rerender } = renderHook(({ cursor }) => useHistoryStream(true, 'u', START, END, cursor), { initialProps: { cursor: START } });
  await waitFor(() => expect(getPlayerTimeline).toHaveBeenCalledTimes(3));
  for (let i = 1; i <= 5; i++) {
    rerender({ cursor: START + i * CHUNK });
    await waitFor(() => expect(result.current.loadedEnd).toBe(START + (i + 2) * CHUNK + PAD));
    expect(result.current.loadedEnd! - result.current.loadedStart!).toBeLessThanOrEqual(3 * CHUNK + 2 * PAD);
  }
  const calls = vi.mocked(getPlayerTimeline).mock.calls.length;
  rerender({ cursor: START });
  await waitFor(() => expect(getPlayerTimeline).toHaveBeenCalledTimes(calls + 2));
  expect(result.current.loadedStart).toBe(START);
});

it('does not leak responses across player or revision changes and cancels on inactivity', async () => {
  const old = deferred<PlayerTimelineResponse>();
  vi.mocked(getPlayerTimeline).mockReturnValueOnce(old.promise).mockResolvedValue(response([], 'b'));
  const { result, rerender } = renderHook(({ user, revision, active }) => useHistoryStream(active, user, START, END, null, revision), { initialProps: { user: 'u', revision: 0, active: true } });
  const signal = vi.mocked(getPlayerTimeline).mock.calls[0][4];
  rerender({ user: 'b', revision: 0, active: true });
  expect(signal?.aborted).toBe(true);
  await act(async () => old.resolve(response([row(START)])));
  await waitFor(() => expect(result.current.data?.user_id).toBe('b'));
  rerender({ user: 'b', revision: 1, active: true });
  await waitFor(() => expect(getPlayerTimeline).toHaveBeenCalledTimes(3));
  rerender({ user: 'b', revision: 1, active: false });
  expect(result.current.buffering).toBe(false);
});

it('handles empty and single-instant complete windows', async () => {
  vi.mocked(getPlayerTimeline).mockResolvedValue({ ...response([row(START)]), range_start: iso(START), range_end: iso(START) });
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, END, null));
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(result.current.data?.trajectories).toHaveLength(1);
  expect(result.current.rangeStart).toBe(START);
  expect(result.current.rangeEnd).toBe(START);
  expect(result.current.buffering).toBe(false);
});

it('does not expose requested window bounds as observations during bootstrap or for empty history', async () => {
  const pending = deferred<PlayerTimelineResponse>();
  vi.mocked(getPlayerTimeline).mockReturnValueOnce(pending.promise);
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, END, null));
  expect(result.current.rangeStart).toBeUndefined();
  expect(result.current.rangeEnd).toBeUndefined();
  await act(async () => pending.resolve(response()));
  expect(result.current.rangeStart).toBeUndefined();
  expect(result.current.rangeEnd).toBeUndefined();
  expect(result.current.data?.trajectories).toEqual([]);
  expect(result.current.buffering).toBe(false);
  expect(result.current.loadedStart).toBe(START);
  expect(result.current.loadedEnd).toBe(END);
});

it('uses limit2000 for unsplittable timestamps and errors explicitly if still truncated', async () => {
  const instant = START;
  vi.mocked(getPlayerTimeline).mockResolvedValue({ ...response([row(instant)]), trajectory_total: 2500, range_start: iso(instant), range_end: iso(instant) });
  const { result } = renderHook(() => useHistoryStream(true, 'u', instant, instant, null));
  await waitFor(() => expect(result.current.error).toBeTruthy());
  expect(vi.mocked(getPlayerTimeline).mock.calls.map(c => c[3])).toEqual([1, 500, 2000]);
  expect(result.current.buffering).toBe(true);
  expect(result.current.data).toBeUndefined();
});

it('surfaces request-budget overflow without publishing an incomplete chunk', async () => {
  vi.mocked(getPlayerTimeline).mockResolvedValue({ ...metadata(), trajectory_total: 20000 });
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, END, START));
  await waitFor(() => expect(result.current.error).toBeTruthy());
  expect(getPlayerTimeline).toHaveBeenCalledTimes(17); // metadata + bounded chunk work
  expect(result.current.data).toBeUndefined();
  expect(result.current.buffering).toBe(true);
});

it('does not request invalid or inactive windows', () => {
  const { rerender } = renderHook(({ active, user, start, end }) => useHistoryStream(active, user, start, end, null), { initialProps: { active: false, user: 'u', start: START, end: END } });
  rerender({ active: true, user: '', start: START, end: END });
  rerender({ active: true, user: 'u', start: NaN, end: END });
  rerender({ active: true, user: 'u', start: END, end: START });
  expect(getPlayerTimeline).not.toHaveBeenCalled();
});

it('fetches explicit cursors beyond position evidence and reports queried coverage for progress replay', async () => {
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce({ ...metadata(), range_end: iso(START + CHUNK) }).mockResolvedValue(response());
  const cursor = START + 8 * CHUNK;
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, END, cursor));
  await waitFor(() => expect(getPlayerTimeline).toHaveBeenCalledTimes(3));
  expect(result.current.rangeEnd).toBe(START + CHUNK);
  expect(vi.mocked(getPlayerTimeline).mock.calls[1][1]).toBe(iso(cursor - PAD));
  expect(result.current.loadedStart).toBe(cursor - PAD);
  expect(result.current.loadedEnd).toBe(cursor + 2 * CHUNK + PAD);
  expect(result.current.buffering).toBe(false);
});

it('recovers a dense single timestamp with the final2000-row request', async () => {
  const rows = Array.from({ length: 1500 }, (_, i) => row(START, String(i).padStart(4, '0')));
  vi.mocked(getPlayerTimeline).mockImplementation(async (_u, _a, _b, limit) => ({ ...response(rows.slice(0, limit)), trajectory_total: rows.length }));
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, START, null));
  await waitFor(() => expect(result.current.data?.trajectories).toHaveLength(1500));
  expect(vi.mocked(getPlayerTimeline).mock.calls.map(c => c[3])).toEqual([1, 500, 2000]);
  expect(result.current.error).toBeUndefined();
});

it('merges duplicate event IDs once and sorts event timestamps across overlapping windows', async () => {
  const early = { id: 'early', occurred_at: iso(START), observed_at: iso(START), event_type: 'join', source: 'guard' as const, confidence: 'observed' as const, summary: '' };
  const late = { ...early, id: 'late', occurred_at: iso(START + CHUNK) };
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce(metadata()).mockResolvedValue({ ...response(), events: [late, early], event_total: 2 });
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, END, null));
  await waitFor(() => expect(getPlayerTimeline).toHaveBeenCalledTimes(3));
  expect(result.current.data?.events.map(e => e.id)).toEqual(['early', 'late']);
  expect(result.current.data?.event_total).toBe(2);
});

it('rejects row-budget overflow without exposing a partial replay', async () => {
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce(metadata()).mockResolvedValue(response(Array.from({ length: 10001 }, (_, i) => row(START + i))));
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, END, null));
  await waitFor(() => expect(result.current.error).toContain('记录上限'));
  expect(result.current.data).toBeUndefined();
  expect(result.current.buffering).toBe(true);
});

it('shows an explicit timeout instead of treating an unfinished chunk as empty', async () => {
  vi.useFakeTimers();
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce(metadata()).mockImplementation((_u, _a, _b, _limit, signal) => new Promise((_resolve, reject) => {
    signal?.addEventListener('abort', () => reject(new Error('aborted')));
  }));
  const { result } = renderHook(() => useHistoryStream(true, 'u', START, END, null));
  await act(async () => {});
  await act(async () => { await vi.advanceTimersByTimeAsync(20000); });
  expect(result.current.error).toContain('超时');
  expect(result.current.buffering).toBe(true);
  expect(result.current.data).toBeUndefined();
});

it('prioritizes a missing target when seeking back one chunk over a pending target+1 request', async () => {
  const obsolete = deferred<PlayerTimelineResponse>();
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce(metadata()).mockReturnValueOnce(obsolete.promise).mockResolvedValue(response());
  const { result, rerender } = renderHook(({ cursor }) => useHistoryStream(true, 'u', START, END, cursor), { initialProps: { cursor: START + 5 * CHUNK } });
  await waitFor(() => expect(getPlayerTimeline).toHaveBeenCalledTimes(2));
  const signal = vi.mocked(getPlayerTimeline).mock.calls[1][4];
  rerender({ cursor: START + 4 * CHUNK });
  expect(signal?.aborted).toBe(true);
  expect(result.current.buffering).toBe(true);
  await act(async () => obsolete.resolve(response()));
  await waitFor(() => expect(result.current.buffering).toBe(false));
  expect(vi.mocked(getPlayerTimeline).mock.calls[2][1]).toBe(iso(START + 4 * CHUNK - PAD));
});

it('pauses the real playback clock on a delayed next chunk and resumes without another play click', async () => {
  vi.useFakeTimers();
  const pending = deferred<PlayerTimelineResponse>();
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce(metadata()).mockResolvedValueOnce(response([row(START)]))
    .mockReturnValueOnce(pending.promise).mockResolvedValue(response());
  const { result } = renderHook(() => {
    const [cursor, setCursor] = useState<number | null>(null);
    const history = useHistoryStream(true, 'u', START, END, cursor);
    const clock = usePlaybackClock(START, END, 'same-window', { buffering: history.buffering, bufferedUntil: history.loadedEnd });
    useEffect(() => {
      if (!history.loading && (cursor === null || Math.floor(cursor / CHUNK) !== Math.floor(clock.time / CHUNK))) setCursor(clock.time);
    }, [history.loading, cursor, clock.time]);
    return { history, clock };
  });
  await act(async () => {});
  expect(getPlayerTimeline).toHaveBeenCalledTimes(3);
  act(() => result.current.clock.seek(START + CHUNK - 1000));
  act(() => result.current.clock.setPlaying(true));
  await act(async () => { await vi.advanceTimersByTimeAsync(100); });
  expect(result.current.history.buffering).toBe(true);
  expect(result.current.clock.playing).toBe(true);
  const pausedAt = result.current.clock.time;
  expect(pausedAt).toBeGreaterThanOrEqual(START + CHUNK);
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(result.current.clock.time).toBe(pausedAt);
  expect(vi.mocked(getPlayerTimeline).mock.calls[2][4]?.aborted).toBe(false);
  await act(async () => pending.resolve(response([row(START + CHUNK)])));
  expect(result.current.history.buffering).toBe(false);
  await act(async () => { await vi.advanceTimersByTimeAsync(100); });
  expect(result.current.clock.time).toBeGreaterThan(pausedAt);
  expect(result.current.clock.playing).toBe(true);
});
