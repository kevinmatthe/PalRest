import { render, screen, within } from '@testing-library/react';
import { expect, it } from 'vitest';
import { JourneyTrend } from './JourneyTrend';

it('keeps compatible runs disconnected and exposes every actual observation as a table', () => {
  const { container } = render(<JourneyTrend label="拥有帕鲁" start={1000} end={8000} runs={[
    [{ checkpointID: 1, time: 1000, value: 4 }, { checkpointID: 2, time: 2000, value: 5 }],
    [{ checkpointID: 3, time: 6000, value: 9 }, { checkpointID: 4, time: 7000, value: 10 }],
  ]} />);
  expect(container.querySelectorAll('polyline')).toHaveLength(2);
  expect(container.querySelectorAll('circle')).toHaveLength(4);
  for (const line of container.querySelectorAll('polyline')) expect(line).toHaveAttribute('stroke-dasharray');
  const table = screen.getByRole('table', { name: '拥有帕鲁原始观测' });
  expect(within(table).getAllByRole('row')).toHaveLength(5);
  expect(screen.getByRole('img', { name: /拥有帕鲁.*4 个观测.*2 段/ })).toBeInTheDocument();
});

it('keeps a single observation as one dot without a fabricated line or zero', () => {
  const { container } = render(<JourneyTrend label="图鉴解锁" start={1000} end={8000} runs={[[{ checkpointID: 8, time: 2000, value: 12 }]]} />);
  expect(container.querySelectorAll('polyline')).toHaveLength(0);
  expect(container.querySelectorAll('circle')).toHaveLength(1);
  expect(within(screen.getByRole('table')).getByText('12')).toBeInTheDocument();
});

it('does not draw an empty metric as a zero trend', () => {
  const { container } = render(<JourneyTrend label="累计捕获记录" start={0} end={1000} runs={[]} />);
  expect(screen.getByText('尚无可绘制的观测')).toBeInTheDocument();
  expect(container.querySelector('svg')).toBeNull();
});
