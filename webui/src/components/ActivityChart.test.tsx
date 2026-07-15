import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ActivityChart } from './ActivityChart';

afterEach(() => vi.useRealTimers());

describe('ActivityChart', () => {
  it('breaks line geometry around missing samples and always shows numeric stats', () => {
    render(<ActivityChart kind="line" label="Concurrent players" unit="人" points={[
      { at: '10:00', value: 2 }, { at: '10:05', value: null }, { at: '10:10', value: 4 },
    ]} />);

    expect(screen.getByRole('img', { name: 'Concurrent players' })).toBeInTheDocument();
    expect(screen.getAllByTestId('line-segment')).toHaveLength(2);
    expect(screen.getByText('最新').parentElement).toHaveTextContent('4 人');
    expect(screen.getByText('最小').parentElement).toHaveTextContent('2 人');
    expect(screen.getByText('最大').parentElement).toHaveTextContent('4 人');
    expect(screen.queryByRole('row', { name: /10:00/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '显示数据表' }));
    expect(screen.getByRole('row', { name: '10:00 2 人' })).toBeInTheDocument();
    expect(screen.getByRole('row', { name: '10:05 缺失' })).toBeInTheDocument();
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
    fireEvent.click(screen.getByRole('button', { name: '显示数据表' }));
    expect(screen.getByRole('row', { name: '2026-07-10 0 ms' })).toBeInTheDocument();
    expect(screen.getByRole('row', { name: '2026-07-11 3600000 ms' })).toBeInTheDocument();
  });

  it('defers large exact-value tables and removes them when hidden', () => {
    const points = Array.from({ length: 200 }, (_, index) => ({ at: `point-${index}`, value: index % 5 ? index : null }));
    const { container } = render(<ActivityChart kind="line" label="Large series" points={points} />);
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(container.querySelector('desc')?.textContent?.length).toBeLessThan(150);
    const button = screen.getByRole('button', { name: '显示数据表' });
    expect(button).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(button);
    expect(screen.getAllByRole('row')).toHaveLength(201);
    expect(screen.getByRole('row', { name: 'point-0 缺失' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '隐藏数据表' }));
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('marks refreshed data as updating for 550ms', () => {
    vi.useFakeTimers();
    const { container, rerender } = render(<ActivityChart kind="line" label="Players" points={[{ at: 'a', value: 1 }]} />);
    rerender(<ActivityChart kind="line" label="Players" points={[{ at: 'b', value: 2 }]} />);
    expect(container.firstChild).toHaveClass('is-updating');
    expect(container.querySelector('.chart-plot')).toContainElement(screen.getByRole('img', { name: 'Players' }));
    act(() => vi.advanceTimersByTime(550));
    expect(container.firstChild).not.toHaveClass('is-updating');
  });

  it('keeps the newest rapid refresh updating for its full duration', () => {
    vi.useFakeTimers();
    const { container, rerender, unmount } = render(<ActivityChart kind="line" label="Players" points={[{ at: 'a', value: 1 }]} />);
    rerender(<ActivityChart kind="line" label="Players" points={[{ at: 'b', value: 2 }]} />);
    act(() => vi.advanceTimersByTime(400));
    rerender(<ActivityChart kind="line" label="Players" points={[{ at: 'c', value: 3 }]} />);
    act(() => vi.advanceTimersByTime(150));
    expect(container.firstChild).toHaveClass('is-updating');
    act(() => vi.advanceTimersByTime(400));
    expect(container.firstChild).not.toHaveClass('is-updating');
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('does not animate when reduced motion is preferred', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
    const { container, rerender } = render(<ActivityChart kind="line" label="Players" points={[{ at: 'a', value: 1 }]} />);
    rerender(<ActivityChart kind="line" label="Players" points={[{ at: 'b', value: 2 }]} />);
    expect(container.firstChild).not.toHaveClass('is-updating');
    vi.unstubAllGlobals();
  });

  it('renders and updates a 30-day five-minute series without retaining a hidden geometry layer', () => {
    const points = Array.from({ length: 8_640 }, (_, index) => ({ at: `p${index}`, value: index % 17 ? index % 8 : null }));
    const { container, rerender } = render(<ActivityChart kind="line" label="Large concurrency" points={points} />);
    expect(screen.getByRole('img', { name: 'Large concurrency' })).toBeInTheDocument();
    expect(container.querySelector('.activity-chart__previous')).not.toBeInTheDocument();
    rerender(<ActivityChart kind="line" label="Large concurrency" points={points.map((point, index) => ({ ...point, value: index % 19 ? point.value : null }))} />);
    expect(container.innerHTML).not.toContain('NaN');
    expect(container.querySelector('.activity-chart__previous')).not.toBeInTheDocument();
  });
});
