import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useJourneyCursor } from './useJourneyCursor';

afterEach(() => vi.useRealTimers());

it('projects fast playback once per wall second and applies pause/seek immediately', () => {
  vi.useFakeTimers();
  const { result, rerender } = renderHook(({ time, playing }) => useJourneyCursor(time, playing, 'u:window'), { initialProps: { time: 1000, playing: true } });
  for (let i = 1; i <= 59; i++) {
    rerender({ time: 1000 + i * 8000, playing: true });
    act(() => { vi.advanceTimersByTime(16); });
    expect(result.current).toBe(1000);
  }
  act(() => { vi.advanceTimersByTime(56); });
  expect(result.current).toBe(473000);
  rerender({ time: 3200, playing: false });
  expect(result.current).toBe(3200);
  act(() => { vi.advanceTimersByTime(2000); });
  expect(result.current).toBe(3200);
});

it('never exposes a future cursor on rewind and resets with the observation window', () => {
  vi.useFakeTimers();
  const { result, rerender } = renderHook(({ time, key }) => useJourneyCursor(time, true, key), { initialProps: { time: 10000, key: 'old' } });
  rerender({ time: 2000, key: 'old' });
  expect(result.current).toBe(2000);
  rerender({ time: 3000, key: 'new' });
  expect(result.current).toBe(3000);
});
