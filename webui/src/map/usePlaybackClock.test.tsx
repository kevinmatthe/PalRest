import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { usePlaybackClock } from './usePlaybackClock';

afterEach(() => vi.useRealTimers());
it('advances continuously, preserves pause/seek, changes speed and stops at the end', () => {
  vi.useFakeTimers();
  const { result } = renderHook(() => usePlaybackClock(1000, 601000, 'window'));
  act(() => result.current.setPlaying(true));
  act(() => { vi.advanceTimersByTime(1000); });
  expect(result.current.time).toBeGreaterThan(55000);
  expect(result.current.time).toBeLessThan(62000);
  act(() => result.current.setPlaying(false));
  const paused = result.current.time;
  act(() => { vi.advanceTimersByTime(1000); });
  expect(result.current.time).toBe(paused);
  act(() => { result.current.seek(121000); result.current.setSpeed(4); });
  expect(result.current.playing).toBe(false);
  act(() => result.current.setPlaying(true));
  act(() => { vi.advanceTimersByTime(1000); });
  expect(result.current.time).toBeGreaterThan(350000);
  act(() => { vi.advanceTimersByTime(2000); });
  expect(result.current.time).toBe(601000);
  expect(result.current.playing).toBe(false);
});

it('waits at the loaded boundary without losing play intent and resumes when data arrives', () => {
  vi.useFakeTimers();
  const { result, rerender } = renderHook(({ bufferedUntil, buffering }) => usePlaybackClock(1000, 601000, 'window', { bufferedUntil, buffering }),
    { initialProps: { bufferedUntil: 31000, buffering: false } });
  act(() => result.current.setPlaying(true));
  act(() => { vi.advanceTimersByTime(1000); });
  expect(result.current.time).toBe(31000);
  expect(result.current.playing).toBe(true);
  rerender({ bufferedUntil: 31000, buffering: true });
  act(() => { vi.advanceTimersByTime(10000); });
  expect(result.current.time).toBe(31000);
  rerender({ bufferedUntil: 121000, buffering: false });
  act(() => { vi.advanceTimersByTime(1000); });
  expect(result.current.time).toBeGreaterThan(85000);
  expect(result.current.time).toBeLessThan(95000);
});

it('preserves the cursor when metadata expands bounds and resets only for a new window', () => {
  const { result, rerender } = renderHook(({ start, end, key }) => usePlaybackClock(start, end, key), { initialProps: { start: 1000, end: 100000, key: 'a' } });
  act(() => result.current.seek(50000));
  rerender({ start: 0, end: 200000, key: 'a' });
  expect(result.current.time).toBe(50000);
  rerender({ start: 2000, end: 200000, key: 'b' });
  expect(result.current.time).toBe(2000);
});
