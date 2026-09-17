import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, afterEach, expect, it, vi } from 'vitest';
import * as api from '../api';
import { MapWorkspace } from './MapWorkspace';

vi.mock('../api', async load => ({ ...await load<typeof import('../api')>(), getLivePositions: vi.fn(), getPlayerTimeline: vi.fn(), getGuildBases: vi.fn(), getPlayerProgress: vi.fn() }));
vi.mock('../map/WorldMap', () => ({ WorldMap: ({ mode, points, heat, focusArea, onSelect, onInteraction }: any) => <div data-testid="world-map" data-mode={mode} data-points={JSON.stringify(points)} data-heat={JSON.stringify(heat ?? [])} data-focus-area={JSON.stringify(focusArea)}><button onClick={() => onSelect('u')}>地图玩家</button><button onClick={onInteraction}>拖动地图</button></div> }));

const player = { user_id: 'u', name: '测试玩家', account_name: 'tester', player_id: 'p', online: true, enabled: false, exempt: false, used_ms: 0, remaining_ms: 0, limit_ms: 0, strategy: 'fixed', period: 'daily', period_start: '', next_reset: '', warning_before_ms: [], warnings: [] } satisfies api.Player;
const now = Date.now();
const live = { as_of: new Date(now).toISOString(), online_count: 1, positioned: 1, players: [{ user_id: 'u', name: '测试玩家', x: 1000, y: 2000, level: 10 }] };
const history = { user_id: 'u', events: [], private_samples: [], trajectories: [0, 30].map((s, i) => ({ user_id: 'u', observed_at: new Date(now - 60000 + s * 1000).toISOString(), x: 3000 + i * 1000, y: 2000, level: 9, ping: 30, segment_id: 's', runtime_epoch: 1, source_ref: String(i) })) } satisfies api.PlayerTimelineResponse;

beforeEach(() => {
  vi.mocked(api.getPlayerProgress).mockResolvedValue({ user_id: 'u', status: 'not_collected', baseline: null, checkpoints: [], changes: [], checkpoint_total: 0, change_total: 0 });
  vi.mocked(api.getLivePositions).mockResolvedValue(live);
  vi.mocked(api.getPlayerTimeline).mockResolvedValue(history);
  vi.mocked(api.getGuildBases).mockResolvedValue({ source: 'game_data', pois: [] });
});
afterEach(() => { vi.useRealTimers(); vi.clearAllMocks(); });

it('collapses the roster and pauses follow when the map is dragged', async () => {
  render(<MapWorkspace players={[player]} refreshKey={0} />);
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  fireEvent.click(screen.getByRole('button', { name: '跟随玩家' }));
  expect(screen.getByRole('button', { name: '停止跟随' })).toHaveAttribute('aria-pressed', 'true');
  fireEvent.click(screen.getByRole('button', { name: '拖动地图' }));
  expect(screen.getByRole('button', { name: '跟随玩家' })).toHaveAttribute('aria-pressed', 'false');
  fireEvent.click(screen.getByRole('button', { name: '收起玩家列表' }));
  expect(screen.queryByRole('complementary', { name: '玩家列表' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '展开玩家列表' }));
  expect(screen.getByRole('complementary', { name: '玩家列表' })).toBeInTheDocument();
});

it('adds only the selected player’s verified recent change and removes it before its historical endpoint', async () => {
  const checkpoints: api.ProgressCheckpoint[] = [0, 1].map(i => ({ id: i + 1, world_id: 'w', observed_at: new Date(now - 55000 + i * 10000).toISOString(), captured_at: new Date(now - 55000 + i * 10000).toISOString(), source: 'save_import', schema_version: 1, consistent: true, boundary: '', metrics: { owned_pals: { state: 'known', value: 5 + i * 2 }, capture_total: { state: 'unknown' }, paldeck: { state: 'unknown' }, fast_travel: { state: 'unknown' } } }));
  const progress: api.PlayerProgressResponse = { user_id: 'u', status: 'available', baseline: null, checkpoints, changes: [{ id: 1, metric: 'owned_pals', before: 5, after: 7, delta: 2, added: [], removed: [], interval_start: checkpoints[0].observed_at, interval_end: checkpoints[1].observed_at, previous_checkpoint_id: 1, checkpoint_id: 2, rule_version: 1, confidence: 'observed', source: 'save_import' }], checkpoint_total: 2, change_total: 1 };
  let resolveHistoryProgress!: (data: api.PlayerProgressResponse) => void;
  const historyProgress = new Promise<api.PlayerProgressResponse>(resolve => { resolveHistoryProgress = resolve; });
  vi.mocked(api.getPlayerProgress).mockResolvedValueOnce(progress).mockReturnValueOnce(historyProgress);
  render(<MapWorkspace players={[player]} refreshKey={0} />);
  const map = screen.getByTestId('world-map');
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  await waitFor(() => expect(JSON.parse(map.dataset.points!)[0].recentProgress).toContain('拥有帕鲁 5 → 7'));
  expect(api.getPlayerTimeline).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: '历史回放' }));
  const slider = screen.getByRole('slider', { name: '回放时间' });
  await waitFor(() => expect(slider).toBeEnabled());
  fireEvent.change(slider, { target: { value: String(now - 45000) } });
  // Trajectory readiness does not imply that the separate progress request has resolved.
  expect(api.getPlayerProgress).toHaveBeenCalledTimes(2);
  expect(JSON.parse(map.dataset.points!)[0].recentProgress).toBeUndefined();
  await act(async () => { resolveHistoryProgress(progress); });
  expect(JSON.parse(map.dataset.points!)[0].recentProgress).toContain('拥有帕鲁 5 → 7');
  fireEvent.change(slider, { target: { value: String(now - 50000) } });
  expect(JSON.parse(map.dataset.points!)[0].recentProgress).toBeUndefined();
});

it('preserves the map and historical cursor through live refresh, then returns to the latest live data', async () => {
  const { rerender } = render(<MapWorkspace players={[player]} refreshKey={0} />);
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  const map = screen.getByTestId('world-map');
  fireEvent.click(screen.getByRole('button', { name: '历史回放' }));
  await waitFor(() => expect(screen.getByRole('slider', { name: '回放时间' })).toBeEnabled());
  const midpoint = now - 45000;
  fireEvent.change(screen.getByRole('slider', { name: '回放时间' }), { target: { value: String(midpoint) } });
  expect(JSON.parse(map.dataset.points!)[0].x).toBe(3500);
  vi.mocked(api.getLivePositions).mockResolvedValue({ ...live, as_of: new Date(now + 1000).toISOString(), players: [{ ...live.players[0], x: 9000 }] });
  rerender(<MapWorkspace players={[player]} refreshKey={1} />);
  await act(async () => {});
  expect(screen.getByTestId('world-map')).toBe(map);
  expect(screen.getByRole('slider', { name: '回放时间' })).toHaveValue(String(midpoint));
  expect(api.getPlayerTimeline).toHaveBeenCalledTimes(1);
  expect(JSON.parse(map.dataset.points!)[0].x).toBe(3500);
  fireEvent.click(screen.getByRole('button', { name: '回到实时' }));
  expect(map.dataset.mode).toBe('live');
  expect(JSON.parse(map.dataset.points!)[0].x).toBe(9000);
});

it('shows truncated history even when the detail panel is closed', async () => {
  vi.mocked(api.getPlayerTimeline).mockResolvedValue({ ...history, trajectory_total: 1000 });
  render(<MapWorkspace players={[player]} refreshKey={0} />);
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  fireEvent.click(screen.getByRole('button', { name: '历史回放' }));
  expect(await screen.findByText(/当前仅加载部分记录/)).toBeInTheDocument();
});

it('disables playback when the selected player has no history', async () => {
  vi.mocked(api.getPlayerTimeline).mockResolvedValue({ ...history, trajectories: [] });
  render(<MapWorkspace players={[player]} refreshKey={0} />);
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  fireEvent.click(screen.getByRole('button', { name: '历史回放' }));
  expect(await screen.findByText('这个时间段没有位置记录')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '播放' })).toBeDisabled();
});

it('supports meaningful keyboard seeking and an explicit history reload', async () => {
  render(<MapWorkspace players={[player]} refreshKey={0} />);
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  fireEvent.click(screen.getByRole('button', { name: '历史回放' }));
  await waitFor(() => expect(screen.getByRole('slider', { name: '回放时间' })).toBeEnabled());
  const slider = screen.getByRole('slider', { name: '回放时间' });
  const before = Number((slider as HTMLInputElement).value);
  fireEvent.keyDown(slider, { key: 'ArrowRight' });
  expect(slider).toHaveValue(String(before + 1000));
  fireEvent.click(screen.getByRole('button', { name: '重新加载历史' }));
  await waitFor(() => expect(api.getPlayerTimeline).toHaveBeenCalledTimes(2));
});

it('seeks progress change endpoints with no trajectory, and hides future progress on rewind', async () => {
  vi.mocked(api.getPlayerTimeline).mockResolvedValue({ ...history, trajectories: [] });
  const earlier = new Date(now - 120000).toISOString();
  const later = new Date(now - 60000).toISOString();
  vi.mocked(api.getPlayerProgress).mockResolvedValue({ user_id: 'u', status: 'available', baseline: null,
    checkpoints: [{ id: 1, world_id: 'w', observed_at: later, captured_at: later, source: 'save_import', schema_version: 1, consistent: true, boundary: '', metrics: { owned_pals: { state: 'known', value: 45 }, capture_total: { state: 'unknown' }, paldeck: { state: 'unknown' }, fast_travel: { state: 'unsupported' } } }],
    changes: [{ id: 1, metric: 'owned_pals', before: 42, after: 45, delta: 3, added: [], removed: [], interval_start: earlier, interval_end: later, previous_checkpoint_id: 0, checkpoint_id: 1, rule_version: 1, confidence: 'observed', source: 'save_import' }], checkpoint_total: 1, change_total: 1 });
  render(<MapWorkspace players={[player]} refreshKey={0} />);
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  fireEvent.click(await screen.findByRole('button', { name: /拥有帕鲁 42 → 45/ }));
  await waitFor(() => expect(screen.getByRole('slider', { name: '回放时间' })).toHaveValue(String(now - 60000)));
  const slider = screen.getByRole('slider', { name: '回放时间' });
  expect(slider).toBeEnabled();
  expect(screen.getByTestId('world-map').dataset.points).toBe('[]');
  fireEvent.change(slider, { target: { value: String(now - 90000) } });
  expect(screen.queryByText('45')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /拥有帕鲁 42 → 45/ })).not.toBeInTheDocument();
  expect(api.getPlayerProgress).toHaveBeenCalledTimes(2);
});

it('loads the live journey only on demand and reuses historical data while seeking', async () => {
  render(<MapWorkspace players={[player]} refreshKey={0} />);
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  expect(api.getPlayerTimeline).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: '游玩小结' }));
  await waitFor(() => expect(api.getPlayerTimeline).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole('button', { name: '历史回放' }));
  await waitFor(() => expect(screen.getByRole('slider', { name: '回放时间' })).toBeEnabled());
  expect(api.getPlayerTimeline).toHaveBeenCalledTimes(2);
  fireEvent.change(screen.getByRole('slider', { name: '回放时间' }), { target: { value: String(now - 45000) } });
  await act(async () => {});
  expect(api.getPlayerTimeline).toHaveBeenCalledTimes(2);
});

it('clears dwell heat when rewinding before completed observations without remounting the map', async () => {
  vi.mocked(api.getPlayerTimeline).mockResolvedValue({ ...history, trajectories: history.trajectories.map(p => ({ ...p, x: 3000 })) });
  render(<MapWorkspace players={[player]} refreshKey={0} />);
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  const map = screen.getByTestId('world-map');
  fireEvent.click(screen.getByLabelText('停留热度'));
  await waitFor(() => expect(JSON.parse(map.dataset.heat!)).toHaveLength(1));
  fireEvent.click(screen.getByRole('button', { name: '历史回放' }));
  const slider = await screen.findByRole('slider', { name: '回放时间' });
  await waitFor(() => expect(slider).toBeEnabled());
  fireEvent.change(slider, { target: { value: String(now - 60000) } });
  expect(JSON.parse(map.dataset.heat!)).toEqual([]);
  fireEvent.change(slider, { target: { value: String(now - 30000) } });
  await waitFor(() => expect(JSON.parse(map.dataset.heat!)).toHaveLength(1));
  expect(screen.getByTestId('world-map')).toBe(map);
  fireEvent.click(screen.getByLabelText('停留热度'));
  expect(JSON.parse(map.dataset.heat!)).toEqual([]);
});

it('keeps a seek made as soon as asynchronously loaded playback becomes enabled', async () => {
  let resolveHistory!: (data: api.PlayerTimelineResponse) => void;
  vi.mocked(api.getPlayerTimeline).mockReturnValue(new Promise(resolve => { resolveHistory = resolve; }));
  render(<MapWorkspace players={[player]} refreshKey={0} />);
  fireEvent.click(await screen.findByRole('button', { name: /选择玩家 测试玩家/ }));
  fireEvent.click(screen.getByRole('button', { name: '历史回放' }));
  const slider = screen.getByRole('slider', { name: '回放时间' }) as HTMLInputElement;
  // Observe readiness before passive effects flush, as waitFor can do.
  // Resolving outside act deliberately exposes this input/reset ordering.
  const sought = new Promise<void>(resolve => {
    const observer = new MutationObserver(() => {
      if (slider.disabled) return;
      observer.disconnect();
      fireEvent.change(slider, { target: { value: String(now - 45000) } });
      resolve();
    });
    observer.observe(slider, { attributes: true });
  });
  resolveHistory(history);
  await sought;
  await act(async () => {});
  expect(JSON.parse(screen.getByTestId('world-map').dataset.points!)[0].x).toBe(3500);
});
