import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { getPlayerTimeline, type PlayerTimelineResponse } from '../api';
import { useJourneyTimeline } from './useJourneyTimeline';
vi.mock('../api', () => ({ getPlayerTimeline: vi.fn() }));
const response = (user_id: string): PlayerTimelineResponse => ({ user_id, trajectories: [], events: [], private_samples: [], trajectory_total: 0, event_total: 0 });
afterEach(() => { vi.useRealTimers(); vi.resetAllMocks(); Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' }); });

it('polls a bounded rolling window serially and stops while inactive', async () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date('2026-09-17T10:00:00Z'));
  let resolve!: (value: PlayerTimelineResponse) => void;
  vi.mocked(getPlayerTimeline).mockImplementationOnce(() => new Promise(r => { resolve = r; }));
  const { result, rerender } = renderHook(({ active }) => useJourneyTimeline(active, 'a', 3600000), { initialProps: { active: true } });
  expect(getPlayerTimeline).toHaveBeenCalledWith('a', '2026-09-17T09:00:00.000Z', '2026-09-17T10:00:00.000Z', 500, expect.any(AbortSignal));
  await act(async () => { vi.advanceTimersByTime(15000); });
  expect(getPlayerTimeline).toHaveBeenCalledTimes(1);
  await act(async () => resolve(response('a')));
  expect(result.current.data?.user_id).toBe('a');
  vi.mocked(getPlayerTimeline).mockResolvedValue(response('a'));
  await act(async () => { vi.advanceTimersByTime(60000); });
  expect(getPlayerTimeline).toHaveBeenCalledTimes(2);
  expect(result.current.end).toBe(Date.now());
  rerender({ active: false });
  await act(async () => { vi.advanceTimersByTime(120000); });
  expect(getPlayerTimeline).toHaveBeenCalledTimes(2);
});

it('aborts old player queries and prevents stale responses or metadata leaking', async () => {
  let resolve!: (value: PlayerTimelineResponse) => void;
  vi.mocked(getPlayerTimeline).mockImplementationOnce(() => new Promise(r => { resolve = r; }));
  const { result, rerender } = renderHook(({ user }) => useJourneyTimeline(true, user, 3600000), { initialProps: { user: 'a' } });
  const signal = vi.mocked(getPlayerTimeline).mock.calls[0][4];
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce(response('b'));
  rerender({ user: 'b' });
  expect(signal?.aborted).toBe(true);
  expect(result.current.data).toBeUndefined();
  await act(async () => {});
  await act(async () => resolve(response('a')));
  expect(result.current.data?.user_id).toBe('b');
});

it('pauses and aborts on a hidden tab then refreshes when visible', async () => {
  vi.mocked(getPlayerTimeline).mockImplementation(() => new Promise(() => {}));
  renderHook(() => useJourneyTimeline(true, 'a', 3600000));
  const signal = vi.mocked(getPlayerTimeline).mock.calls[0][4];
  await act(async () => { Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); document.dispatchEvent(new Event('visibilitychange')); });
  expect(signal?.aborted).toBe(true);
  await act(async () => { Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' }); document.dispatchEvent(new Event('visibilitychange')); });
  expect(getPlayerTimeline).toHaveBeenCalledTimes(2);
});

it('does not query an empty player or invalid window, and reports timeout', async () => {
  vi.useFakeTimers();
  const { result, rerender } = renderHook(({ user, duration }) => useJourneyTimeline(true, user, duration), { initialProps: { user: '', duration: 3600000 } });
  expect(getPlayerTimeline).not.toHaveBeenCalled();
  rerender({ user: 'a', duration: 32 * 86400000 });
  expect(getPlayerTimeline).not.toHaveBeenCalled();
  vi.mocked(getPlayerTimeline).mockImplementation((_u, _s, _e, _l, signal) => new Promise((_resolve, reject) => signal?.addEventListener('abort', () => reject(new Error('aborted')))));
  rerender({ user: 'a', duration: 3600000 });
  await act(async () => { vi.advanceTimersByTime(20000); });
  expect(result.current.loading).toBe(false);
  expect(result.current.error).toContain('超时');
});
