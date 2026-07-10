import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ActivityChart } from './ActivityChart';

afterEach(() => vi.useRealTimers());

describe('ActivityChart', () => {
  it('breaks line geometry around missing samples and exposes exact values', () => {
    render(<ActivityChart kind="line" label="Concurrent players" points={[
      { at: '10:00', value: 2 }, { at: '10:05', value: null }, { at: '10:10', value: 4 },
    ]} />);

    expect(screen.getByRole('img', { name: 'Concurrent players' })).toBeInTheDocument();
    expect(screen.getAllByTestId('line-segment')).toHaveLength(2);
    expect(screen.getByText('10:00: 2')).toBeInTheDocument();
    expect(screen.getByText('10:05: no data')).toBeInTheDocument();
  });

  it.each([[[]], [[{ at: '10:00', value: null }]]])('never renders invalid geometry for empty/all-null lines', (points) => {
    const { container } = render(<ActivityChart kind="line" label="Empty" points={points} />);
    expect(container.innerHTML).not.toContain('NaN');
  });

  it('keeps ordered zero-value bars and accessible date/duration labels', () => {
    render(<ActivityChart kind="bar" label="Daily playtime" points={[
      { date: '2026-07-10', value: 0 }, { date: '2026-07-11', value: 3_600_000 },
    ]} />);
    expect(screen.getAllByTestId('bar')).toHaveLength(2);
    expect(screen.getByText('2026-07-10: 0 ms')).toBeInTheDocument();
    expect(screen.getByText('2026-07-11: 3600000 ms')).toBeInTheDocument();
  });

  it('marks refreshed data as updating for 550ms', () => {
    vi.useFakeTimers();
    const { container, rerender } = render(<ActivityChart kind="line" label="Players" points={[{ at: 'a', value: 1 }]} />);
    rerender(<ActivityChart kind="line" label="Players" points={[{ at: 'b', value: 2 }]} />);
    expect(container.firstChild).toHaveClass('is-updating');
    act(() => vi.advanceTimersByTime(550));
    expect(container.firstChild).not.toHaveClass('is-updating');
  });

  it('does not animate when reduced motion is preferred', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
    const { container, rerender } = render(<ActivityChart kind="line" label="Players" points={[{ at: 'a', value: 1 }]} />);
    rerender(<ActivityChart kind="line" label="Players" points={[{ at: 'b', value: 2 }]} />);
    expect(container.firstChild).not.toHaveClass('is-updating');
    vi.unstubAllGlobals();
  });
});
