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

it('paginates raw evidence without dropping any observation from a large window', async () => {
  const { fireEvent } = await import('@testing-library/react');
  const points = Array.from({ length: 205 }, (_, i) => ({ checkpointID: i + 1, time: 1000 + i, value: i }));
  render(<JourneyTrend label="经验" start={1000} end={2000} runs={[points]} showTable />);
  const table = screen.getByRole('table');
  expect(within(table).getAllByRole('row')).toHaveLength(101);
  expect(within(table).queryByText('#205')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '下一页观测' }));
  fireEvent.click(screen.getByRole('button', { name: '下一页观测' }));
  expect(within(table).getAllByRole('row')).toHaveLength(6);
  expect(within(table).getByText('#205')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '下一页观测' })).toBeDisabled();
});

it('bounds chart nodes for complete windows while preserving actual extrema and disconnected runs', () => {
  const first = Array.from({ length: 2500 }, (_, i) => ({ checkpointID: i + 1, time: i, value: i === 1200 ? 9999 : 10 }));
  const second = Array.from({ length: 2500 }, (_, i) => ({ checkpointID: i + 2501, time: i + 5000, value: 20 }));
  const { container } = render(<JourneyTrend label="等级" start={0} end={10000} runs={[first, second]} />);
  expect(container.querySelectorAll('circle').length).toBeLessThanOrEqual(1060);
  expect(container.querySelectorAll('polyline')).toHaveLength(2);
  expect(container.querySelector('svg')?.textContent).toContain('9999');
  expect(screen.getByText(/图表抽样显示/)).toBeInTheDocument();
});
