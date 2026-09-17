import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import type { JourneyEdge, JourneyMetric, JourneySummary } from '../map/journeyTypes';
import { WorkspaceJourney } from './WorkspaceJourney';

const start = Date.parse('2026-09-17T10:00:00Z');
const end = start + 600_000;
const unknown = (): JourneyMetric => ({ status: 'unknown', delta: null, latestValue: null, changes: [], runs: [] });
const edge: JourneyEdge = { start, end: start + 300_000, durationMs: 300_000, distance: 1200, stationary: false, from: { x: 10, y: 20, sourceRef: 'position-1' }, to: { x: 30, y: 40, sourceRef: 'position-2' } };
function summary(): JourneySummary {
  return { start, end, position: { sampleCount: 3, totalCount: 3, observedMs: 360_000, unknownMs: 240_000, coverage: .6, movingMs: 300_000, stationaryMs: 60_000, pathLength: 1200, asOf: end - 60_000, ageMs: 60_000, edges: [edge], level: { from: 12, to: 13, delta: 1, start, end: end - 60_000 } }, metrics: { owned_pals: unknown(), capture_total: unknown(), paldeck: unknown(), fast_travel: unknown() }, heat: [], inferences: [], warnings: [] };
}
function mount(data = summary(), extra = {}) {
  const props = { summary: data, name: '测试玩家', loading: false, onSeek: vi.fn(), onFocus: vi.fn(), onRetry: vi.fn(), ...extra };
  render(<WorkspaceJourney {...props} />);
  return props;
}

it('makes coverage, unknown duration, game units and observation freshness explicit', () => {
  mount();
  expect(screen.getByText('观察窗口小结')).toBeInTheDocument();
  expect(screen.getByText(/未知 4 分钟/)).toBeInTheDocument();
  expect(screen.getByRole('meter', { name: '位置观测覆盖率' })).toHaveAttribute('aria-valuenow', '60');
  expect(screen.getByText('1,200')).toBeInTheDocument();
  expect(screen.getByText('游戏单位')).toBeInTheDocument();
  expect(screen.getByText(/距窗口终点 1 分钟/)).toBeInTheDocument();
  expect(screen.getByText('12 → 13')).toBeInTheDocument();
  expect(screen.getByText('证据不足，暂不判断活动类型')).toBeInTheDocument();
  expect(screen.queryByText(/AFK|挂机|米$/)).not.toBeInTheDocument();
});

it('shows unknown quantities without zero and lets the entire panel collapse', () => {
  mount();
  expect(screen.getAllByText('未采集')).toHaveLength(6);
  expect(screen.queryByText('+0')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /观察窗口小结/ }));
  expect(screen.queryByRole('meter')).not.toBeInTheDocument();
});

it('labels partial changes precisely and seeks the saved interval end with checkpoint and set evidence', () => {
  const data = summary();
  data.metrics.owned_pals = { status: 'partial', delta: 2, latestValue: 7, runs: [], changes: [{ id: 42, metric: 'owned_pals', before: 5, after: 7, delta: 2, added: ['Pal_A'], removed: ['Pal_B'], previous_checkpoint_id: 8, checkpoint_id: 9, interval_start: new Date(start).toISOString(), interval_end: new Date(end).toISOString(), confidence: 'observed', source: 'save_import', rule_version: 1 }] };
  const { onSeek, onFocus } = mount(data);
  expect(screen.getByText('已确认变化合计')).toBeInTheDocument();
  expect(screen.queryByText(/全窗净增长/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /拥有帕鲁/ }));
  expect(screen.getByText('Pal_A')).toBeInTheDocument();
  expect(screen.getByText('Pal_B')).toBeInTheDocument();
  expect(screen.getByText(/快照 #8 → #9/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /回看变化 #42/ }));
  expect(onSeek).toHaveBeenCalledWith(end);
  expect(onFocus).not.toHaveBeenCalled();
});

it('explains exploration thresholds and cites actual position and unlock evidence', async () => {
  const data = summary();
  data.inferences = [{ kind: 'exploration', ruleVersion: 1, changeIDs: [43], edges: [edge], observedMs: 360_000, movingMs: 300_000, coverage: .6, unlockedCount: 1 }];
  data.metrics.fast_travel.changes = [{ id: 43, metric: 'fast_travel', before: 2, after: 3, delta: 1, added: ['Travel_A'], removed: [], previous_checkpoint_id: 4, checkpoint_id: 5, interval_start: new Date(start).toISOString(), interval_end: new Date(end).toISOString(), confidence: 'observed', source: 'save_import', rule_version: 1 }];
  mount(data);
  fireEvent.click(screen.getByText('可能有探索活动'));
  expect(screen.getByText(/有效观测 ≥ 5 分钟/)).toBeInTheDocument();
  expect(screen.getByText(/覆盖率 ≥ 60%/)).toBeInTheDocument();
  expect(screen.getByText(/移动 ≥ 2 分钟/)).toBeInTheDocument();
  expect(screen.getByText(/新增传送点 ≥ 1/)).toBeInTheDocument();
  expect(screen.getByText(/传送点解锁 2 → 3/)).toBeInTheDocument();
  const inference = screen.getByRole('region', { name: '活动线索' });
  await userEvent.click(within(inference).getByText('位置观测证据 · 1 个区间'));
  expect(await within(inference).findByText(/position-1/)).toBeInTheDocument();
  expect(screen.queryByText(/概率|置信度/)).not.toBeInTheDocument();
});

it('focuses only an evidenced approximate region and exposes source observations', async () => {
  const data = summary();
  const cell = { id: '1:2', x: 20, y: 30, durationMs: 60_000, start, end: end - 60_000, edges: [edge] };
  data.heat = [cell];
  const { onFocus } = mount(data);
  const regions = screen.getByRole('region', { name: '主要停留区域' });
  fireEvent.click(within(regions).getByRole('button', { name: /定位近似网格区域 1/ }));
  expect(onFocus).toHaveBeenCalledWith(cell);
  await userEvent.click(within(regions).getByText('位置观测证据 · 1 个区间'));
  expect(await within(regions).findByText(/position-1/)).toBeInTheDocument();
});

it('does not imply comparison across a boundary and retries failed requests', () => {
  const data = summary();
  data.metrics.owned_pals = { ...unknown(), status: 'boundary', latestValue: 5 };
  data.warnings = ['timeline_truncated', 'progress_boundary', 'stale_positions'];
  const { onRetry } = mount(data, { error: '读取失败' });
  expect(screen.getByText('不可比较')).toBeInTheDocument();
  expect(screen.getByText(/位置记录未完整加载/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '重试' }));
  expect(onRetry).toHaveBeenCalledOnce();
});

it('distinguishes a missing latest observation from a metric never collected', () => {
  const data = summary();
  data.metrics.owned_pals = { ...unknown(), runs: [[{ checkpointID: 1, time: start, value: 5 }]] };
  mount(data);
  const metric = screen.getByRole('region', { name: '拥有帕鲁趋势与证据' });
  expect(within(metric).getByText('不可比较')).toBeInTheDocument();
  expect(within(metric).queryByText('未采集')).not.toBeInTheDocument();
});

it('ranks only the three longest evidenced regions without changing the input', () => {
  const data = summary();
  data.heat = [1, 4, 2, 3].map(value => ({ id: String(value), x: value * 100, y: 30, durationMs: value * 60_000, start, end, edges: [edge] }));
  const { onFocus } = mount(data);
  const regions = screen.getByRole('region', { name: '主要停留区域' });
  expect(within(regions).getAllByRole('button')).toHaveLength(3);
  fireEvent.click(within(regions).getByRole('button', { name: '定位近似网格区域 1' }));
  expect(onFocus).toHaveBeenCalledWith(data.heat[1]);
  expect(data.heat.map(cell => cell.id)).toEqual(['1', '4', '2', '3']);
});

it('mounts large position evidence only while its disclosure is expanded', async () => {
  const data = summary();
  data.position.edges = Array.from({ length: 300 }, (_, index) => ({ ...edge, start: start + index, from: { ...edge.from, sourceRef: `sample-source-${index}` } }));
  mount(data);
  expect(screen.queryAllByText(/sample-source-/)).toHaveLength(0);
  const disclosure = screen.getByText('位置观测证据 · 300 个区间');
  await userEvent.click(disclosure);
  await waitFor(() => expect(screen.getAllByText(/sample-source-/)).toHaveLength(300));
  await userEvent.click(disclosure);
  await waitFor(() => expect(screen.queryAllByText(/sample-source-/)).toHaveLength(0));
});


it('renders saved growth with its own observation time and expands milestone evidence before seeking', async () => {
  const data = summary();
  const change = { id: 99, metric: 'level' as const, before: 4, after: 5, delta: 1, added: [], removed: [], previous_checkpoint_id: 8, checkpoint_id: 9, interval_start: new Date(start).toISOString(), interval_end: new Date(end - 120_000).toISOString(), confidence: 'observed' as const, source: 'save_import' as const, rule_version: 1 as const };
  data.metrics.level = { status: 'boundary', delta: null, latestValue: 5, runs: [[{ checkpointID: 9, time: end - 120_000, value: 5 }]], changes: [change] };
  data.metrics.experience = { status: 'known', delta: 0, latestValue: 0, runs: [[{ checkpointID: 9, time: end - 120_000, value: 0 }]], changes: [] };
  data.milestones = [change];
  const { onSeek } = mount(data);
  const level = screen.getByRole('region', { name: '存档等级趋势与证据' });
  expect(within(level).getByText('不可比较')).toBeInTheDocument();
  expect(level.querySelector('time')).toHaveAttribute('dateTime', change.interval_end);
  expect(screen.getByText('REST 等级观测变化')).toBeInTheDocument();
  const milestones = screen.getByRole('region', { name: '成长里程碑' });
  expect(within(milestones).queryByText(/快照 #8/)).not.toBeInTheDocument();
  await userEvent.click(within(milestones).getByText('存档等级提升 4 → 5'));
  expect(await within(milestones).findByText(/快照 #8 → #9/)).toBeInTheDocument();
  expect(within(milestones).getByText(/已确认的局部区间/)).toBeInTheDocument();
  fireEvent.click(within(milestones).getByRole('button', { name: /回看变化 #99/ }));
  expect(onSeek).toHaveBeenCalledWith(end - 120_000);
});

it('does not turn REST level changes into saved milestones', () => {
  mount();
  const milestones = screen.getByRole('region', { name: '成长里程碑' });
  expect(within(milestones).getByText('暂无可确认的成长里程碑。')).toBeInTheDocument();
});


it('reveals the exact saved unlock IDs only when milestone evidence is expanded', async () => {
  const data = summary();
  data.milestones = [{ id: 100, metric: 'paldeck', before: 1, after: 2, delta: 1, added: ['Pal_Observed'], removed: [], previous_checkpoint_id: 8, checkpoint_id: 9, interval_start: new Date(start).toISOString(), interval_end: new Date(end).toISOString(), confidence: 'observed', source: 'save_import', rule_version: 1 }];
  const { onSeek } = mount(data);
  const milestones = screen.getByRole('region', { name: '成长里程碑' });
  expect(within(milestones).queryByText('Pal_Observed')).not.toBeInTheDocument();
  await userEvent.click(within(milestones).getByText('图鉴新增 1 项'));
  expect(await within(milestones).findByText('Pal_Observed')).toBeInTheDocument();
  fireEvent.click(within(milestones).getByRole('button', { name: /回看变化 #100/ }));
  expect(onSeek).toHaveBeenCalledWith(end);
});

it('shows legacy point counts and last coordinates while explaining missing continuity', () => {
  const data = summary();
  Object.assign(data.position, { observedMs: 0, coverage: 0, movingMs: 0, stationaryMs: 0, pathLength: 0, edges: [], level: null, lastObservation: { x: 13000, y: 100 } });
  data.warnings = ['position_continuity_unknown'];
  mount(data);
  expect(screen.getByText('已加载位置观测 3 个')).toBeInTheDocument();
  expect(screen.getByText('最后观测坐标 (13,000, 100)')).toBeInTheDocument();
  expect(screen.getByText(/连续性信息不足/)).toBeInTheDocument();
});
