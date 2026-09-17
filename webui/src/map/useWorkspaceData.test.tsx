import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { getLivePositions, getPlayerTimeline } from '../api';
import { useLivePositions, useHistoryWindow } from './useWorkspaceData';

vi.mock('../api', () => ({ getLivePositions: vi.fn(), getPlayerTimeline: vi.fn() }));
afterEach(() => { vi.useRealTimers(); vi.resetAllMocks(); });
const live = { as_of: '2026-09-16T10:00:00Z', online_count: 1, positioned: 1, players: [{ user_id: 'u', name: '玩家', x: 1000, y: 2000 }] };

it('serializes polling, retains last good state after failure and aborts on unmount', async () => {
  vi.useFakeTimers();
  let resolve!: (data: typeof live) => void;
  vi.mocked(getLivePositions).mockImplementationOnce(() => new Promise(r => { resolve = r; }));
  const { result, unmount } = renderHook(() => useLivePositions(0));
  await act(async () => { vi.advanceTimersByTime(5000); });
  expect(getLivePositions).toHaveBeenCalledTimes(1);
  await act(async () => { resolve(live); });
  expect(result.current.data).toEqual(live);
  vi.mocked(getLivePositions).mockRejectedValueOnce(new Error('连接中断'));
  await act(async () => { vi.advanceTimersByTime(1000); });
  expect(result.current.data).toEqual(live);
  expect(result.current.error).toBe('连接中断');
  vi.mocked(getLivePositions).mockImplementation(() => new Promise(() => {}));
  await act(async () => { vi.advanceTimersByTime(1000); });
  const signal = vi.mocked(getLivePositions).mock.calls.at(-1)?.[0];
  unmount();
  expect(signal?.aborted).toBe(true);
});

it('ignores an older live observation after manual refresh', async () => {
  vi.useFakeTimers();
  vi.mocked(getLivePositions).mockResolvedValueOnce(live);
  const { result, rerender } = renderHook(({ key }) => useLivePositions(key), { initialProps: { key: 0 } });
  await act(async () => {});
  vi.mocked(getLivePositions).mockResolvedValueOnce({ ...live, as_of: '2026-09-16T09:00:00Z', players: [] });
  rerender({ key: 1 });
  await act(async () => {});
  expect(result.current.data?.players).toHaveLength(1);
});

it('does not let a previous player request populate the new selection', async () => {
  let resolve!: (data: any) => void;
  vi.mocked(getPlayerTimeline).mockImplementationOnce(() => new Promise(r => { resolve = r; }));
  const { result, rerender } = renderHook(({ user }) => useHistoryWindow(true, user, 1000, 2000), { initialProps: { user: 'a' } });
  const firstSignal = vi.mocked(getPlayerTimeline).mock.calls[0][4];
  vi.mocked(getPlayerTimeline).mockResolvedValueOnce({ user_id: 'b', events: [], trajectories: [], private_samples: [] });
  rerender({ user: 'b' });
  expect(firstSignal?.aborted).toBe(true);
  await act(async () => {});
  await act(async () => { resolve({ user_id: 'a', events: [], trajectories: [], private_samples: [] }); });
  expect(result.current.data?.user_id).toBe('b');
});

it('reuses a loaded historical window when returning from live mode', async () => {
  vi.mocked(getPlayerTimeline).mockResolvedValue({ user_id: 'a', events: [], trajectories: [], private_samples: [] });
  const { rerender } = renderHook(({ active }) => useHistoryWindow(active, 'a', 1000, 2000), { initialProps: { active: true } });
  await act(async () => {});
  rerender({ active: false });
  rerender({ active: true });
  await act(async () => {});
  expect(getPlayerTimeline).toHaveBeenCalledTimes(1);
});
