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
