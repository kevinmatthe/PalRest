import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import type { Player, PlayerTimelineResponse } from '../api';
import { MAP_LANDMARKS } from '../map/mapLandmarks';
import { PlayerTimeline, tileErrorTransition } from './PlayerTimeline';

vi.mock('../api', async (load) => ({
  ...(await load<typeof import('../api')>()),
  getPlayerTimeline: vi.fn(),
  getPlayerWorldPOIs: vi.fn().mockResolvedValue({ user_id: '', source: 'none', pois: [] }),
}));

const players = [
  { user_id: 'u/1', player_id: 'p1', name: 'Avery', account_name: 'trail', online: true } as Player,
  { user_id: 'u2', player_id: 'p2', name: 'Morgan', account_name: 'camp' } as Player,
];
const empty: PlayerTimelineResponse = { user_id: 'u/1', events: [], trajectories: [], private_samples: [] };

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

function localInputValueForTest(date: Date) {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

/** Detail list is collapsed by default — expand when a test needs spine rows. */
async function expandEvidenceDetail() {
  const toggle = await screen.findByRole('button', { name: /证据明细/i });
  if (toggle.getAttribute('aria-expanded') !== 'true') {
    fireEvent.click(toggle);
  }
  return toggle;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getPlayerTimeline).mockResolvedValue(empty);
  vi.mocked(api.getPlayerWorldPOIs).mockResolvedValue({ user_id: '', source: 'none', pois: [] });
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe('PlayerTimeline', () => {
  it('starts intentionally empty and offers accessible player and range controls', () => {
    render(<PlayerTimeline includePrivate players={players} refreshKey={0} />);
    expect(screen.getByText(/选择玩家后查看轨迹和事件/)).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: /玩家/i })).toHaveValue('');
    expect(screen.getByLabelText(/开始/i)).toHaveAttribute('type', 'datetime-local');
    expect(screen.getByLabelText(/结束/i)).toHaveAttribute('type', 'datetime-local');
    expect(api.getPlayerTimeline).not.toHaveBeenCalled();
  });

  it('selects a known player and requests its RFC3339 timeline', async () => {
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(api.getPlayerTimeline).toHaveBeenCalledTimes(1));
    expect(api.getPlayerTimeline).toHaveBeenCalledWith('u/1', expect.stringMatching(/T.*(?:Z|[+-]\d\d:\d\d)$/), expect.stringMatching(/T.*(?:Z|[+-]\d\d:\d\d)$/), 500, expect.any(AbortSignal), false);
    expect(await screen.findByText(/当前时间范围没有观察记录/)).toBeInTheDocument();
  });

  it('aborts replaced work and ignores stale responses', async () => {
    const first = deferred<PlayerTimelineResponse>();
    const second = deferred<PlayerTimelineResponse>();
    vi.mocked(api.getPlayerTimeline).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(api.getPlayerTimeline).toHaveBeenCalledTimes(1));
    const firstSignal = vi.mocked(api.getPlayerTimeline).mock.calls[0]?.[4];
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u2' } });
    expect(firstSignal?.aborted).toBe(true);
    second.resolve({ ...empty, user_id: 'u2' });
    await screen.findByText(/当前时间范围没有观察记录/);
    first.resolve({ ...empty, events: [{ id: 'late', event_type: 'player_joined', occurred_at: '2026-07-13T08:00:00Z', observed_at: '2026-07-13T08:00:00Z', source: 'guard', confidence: 'observed', summary: 'joined' }] });
    expect(screen.queryByText('joined')).not.toBeInTheDocument();
  });

  it('merges observations chronologically without mutating inputs and marks private telemetry', async () => {
    const payload: PlayerTimelineResponse = {
      user_id: 'u/1',
      events: [
        { id: 'future', event_type: 'future_event', occurred_at: '2026-07-13T10:00:00Z', observed_at: '2026-07-13T10:03:00Z', source: 'save_snapshot', confidence: 'snapshot_derived', summary: 'unsupported event payload' },
        { id: 'joined', event_type: 'player_joined', occurred_at: '2026-07-13T08:00:00Z', observed_at: '2026-07-13T08:01:00Z', source: 'palworld_rest', confidence: 'observed', summary: 'Avery joined', data: { name: 'Avery' } },
      ],
      trajectories: [
        { user_id: 'u/1', segment_id: 's2', observed_at: '2026-07-13T09:00:00Z', x: 123.45, y: -9.5, ping: 20, level: 12, source_ref: 'poll-2', runtime_epoch: 1 },
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T08:30:00Z', x: 10, y: 20, ping: 18, level: 12, source_ref: 'poll-1', runtime_epoch: 0 },
      ],
      private_samples: [{ user_id: 'u/1', observed_at: '2026-07-13T08:45:00Z', ip: '2001:db8::1:8211', ping: 17, level: 12, source_ref: 'private-1' }],
    };
    const original = JSON.stringify(payload);
    vi.mocked(api.getPlayerTimeline).mockResolvedValue(payload);
    render(<PlayerTimeline includePrivate players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await expandEvidenceDetail();

    expect(await screen.findByText('玩家加入')).toBeInTheDocument();
    const items = screen.getAllByRole('listitem').map((node) => node.textContent);
    expect(items.join('|')).toMatch(/玩家加入.*10.*20.*2001:db8::1:8211.*123.45.*-9.5.*未知事件/i);
    expect(screen.getByText(/IP · 管理员私有/i)).toBeInTheDocument();
    expect(screen.getAllByText(/^坐标$/i)).toHaveLength(2);
    expect(screen.getAllByText(/REST/i).length).toBeGreaterThan(0);
    expect(screen.getByText('存档')).toBeInTheDocument();
    expect(screen.getAllByText(/发生时间/i).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/观测时间/i).length).toBeGreaterThan(0);
    const segment = screen.getByText(/新轨迹段/i).closest('li');
    expect(segment?.previousElementSibling).toHaveTextContent('2001:db8::1:8211');
    expect(segment?.nextElementSibling).toHaveTextContent('123.45, -9.5');
    const privateEntry = screen.getByText(/IP · 管理员私有/i).closest('li');
    expect(privateEntry).not.toBeNull();
    expect(within(privateEntry!).getByText('REST')).toBeInTheDocument();
    expect(within(privateEntry!).queryByText('守护器')).not.toBeInTheDocument();
    expect(JSON.stringify(payload)).toBe(original);
    expect(screen.queryByText('future_event')).not.toBeInTheDocument();
  });

  it('keeps private samples out of the public timeline even if the server sends them', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      private_samples: [{ user_id: 'u/1', observed_at: '2026-07-13T08:00:00Z', ip: '192.0.2.1:8211', ping: 10, level: 2, source_ref: 'private' }],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    expect(await screen.findByText(/当前时间范围没有观察记录/)).toBeInTheDocument();
    expect(screen.queryByText('192.0.2.1:8211')).not.toBeInTheDocument();
    expect(screen.queryByText(/管理员私有视图/i)).not.toBeInTheDocument();
  });

  it('labels unified guard result and player attribute events', async () => {
	vi.mocked(api.getPlayerTimeline).mockResolvedValue({
	  ...empty,
	  events: [
		{ id: 'warning', event_type: 'guard_warning_failed', occurred_at: '2026-07-13T08:00:00Z', observed_at: '2026-07-13T08:00:00Z', source: 'guard', confidence: 'observed', summary: 'guard_warning_failed', data: { error_code: 'delivery_failed' } },
		{ id: 'kick', event_type: 'enforcement_succeeded', occurred_at: '2026-07-13T08:01:00Z', observed_at: '2026-07-13T08:01:00Z', source: 'guard', confidence: 'observed', summary: 'enforcement_succeeded', data: { outcome: 'success' } },
		{ id: 'attribute', event_type: 'player_attribute_changed', occurred_at: '2026-07-13T08:02:00Z', observed_at: '2026-07-13T08:02:00Z', source: 'palworld_rest', confidence: 'observed', summary: 'player_attribute_changed', data: { changes: { level: { old: 41, new: 42 } } } },
	  ],
	});
	render(<PlayerTimeline players={players} refreshKey={0} />);
	fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
	await expandEvidenceDetail();
	expect(await screen.findByText('提醒发送失败')).toBeInTheDocument();
	expect(screen.getByText('限制执行成功')).toBeInTheDocument();
	expect(screen.getByText('玩家属性变更')).toBeInTheDocument();
  });

  it('does not infer a gap from elapsed time when the authoritative segment is unchanged', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      events: [{ id: 'between', event_type: 'player_joined', occurred_at: '2026-07-13T08:59:00Z', observed_at: '2026-07-13T08:59:00Z', source: 'guard', confidence: 'observed', summary: 'joined' }],
      trajectories: [
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T08:00:00Z', x: 1, y: 1, ping: 20, level: 1, source_ref: 'one', runtime_epoch: 0 },
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T09:00:00Z', x: 2, y: 2, ping: 20, level: 1, source_ref: 'two', runtime_epoch: 0 },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await expandEvidenceDetail();
    await screen.findByText('玩家加入');
    expect(screen.queryByText(/Observation gap/i)).not.toBeInTheDocument();
  });

  it('renders trajectory samples as a map replay with a shared cursor', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      events: [{ id: 'joined', event_type: 'player_joined', occurred_at: '2026-07-13T08:01:00Z', observed_at: '2026-07-13T08:01:00Z', source: 'guard', confidence: 'observed', summary: 'joined' }],
      trajectories: [
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T08:00:00Z', x: 100, y: 200, ping: 20, level: 1, source_ref: 'one', runtime_epoch: 0 },
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T08:02:00Z', x: 180, y: 260, ping: 22, level: 1, source_ref: 'two', runtime_epoch: 0 },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });

    const map = await screen.findByTestId('timeline-map');
    expect(map).toBeInTheDocument();
    expect(map).toHaveClass('timeline-leaflet-map');
    const mapReplay = screen.getByLabelText(/地图回放/i);
    expect(screen.getByText('2 个坐标')).toBeInTheDocument();
    expect(mapReplay.querySelector('.leaflet-container')).not.toBeNull();
    expect(screen.getByLabelText(/时间轴光标/i)).toHaveValue('0');
    // Detail list stays collapsed until opened.
    expect(screen.queryByTestId('timeline-spine-window')).not.toBeInTheDocument();
    await expandEvidenceDetail();
    expect(screen.getByText('100, 200')).toBeInTheDocument();
    expect(screen.getByText('180, 260')).toBeInTheDocument();
  });

  it('serves vendored tiles first and falls back to palworld.gg per missing tile', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      trajectories: [
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T08:00:00Z', x: 100, y: 200, ping: 20, level: 1, source_ref: 'one', runtime_epoch: 0 },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    const map = await screen.findByLabelText(/地图回放/i);
    await waitFor(() => {
      const usesLocalTiles = Array.from(map.querySelectorAll<HTMLImageElement>('img')).some((node) => /\/map\/tiles\/\d+\/\d+\/\d+\.png$/.test(node.getAttribute('src') ?? ''));
      expect(usesLocalTiles).toBe(true);
    });

    // First failure retries the same tile against palworld.gg; second gives up.
    const retry = tileErrorTransition(false, { x: 2, y: 3, z: 4 });
    expect(retry).toEqual({ action: 'retry', src: 'https://palworld.gg/images/tiles/4/2/3.png' });
    expect(tileErrorTransition(true, { x: 2, y: 3, z: 4 })).toEqual({ action: 'fail' });
  });

  it('syncs the end time to now when refreshKey changes', async () => {
    const { rerender } = render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByLabelText(/结束/i), { target: { value: '2026-07-01T00:00' } });
    expect(screen.getByLabelText(/结束/i)).toHaveValue('2026-07-01T00:00');
    rerender(<PlayerTimeline players={players} refreshKey={1} />);

    await waitFor(() => expect(screen.getByLabelText(/结束/i)).toHaveValue(localInputValueForTest(new Date())));
  });

  it('shows a segment boundary when the same raw identity belongs to another runtime epoch', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      trajectories: [
        { user_id: 'u/1', segment_id: 'runtime:0:YWJj', observed_at: '2026-07-13T08:00:00Z', x: 1, y: 1, ping: 20, level: 1, source_ref: 'one', runtime_epoch: 0 },
        { user_id: 'u/1', segment_id: 'runtime:1:YWJj', observed_at: '2026-07-13T08:01:00Z', x: 2, y: 2, ping: 20, level: 1, source_ref: 'two', runtime_epoch: 1 },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await expandEvidenceDetail();
    expect(await screen.findByText(/新轨迹段/i)).toBeInTheDocument();
  });

  it('retains the selected player and private context when search excludes that player', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({ ...empty, private_samples: [{ user_id: 'u/1', observed_at: '2026-07-13T08:00:00Z', ip: '192.0.2.1:8211', ping: 10, level: 2, source_ref: 'private' }] });
    render(<PlayerTimeline includePrivate players={players} refreshKey={0} />);
    const select = screen.getByRole('combobox', { name: /玩家/i });
    fireEvent.change(select, { target: { value: 'u/1' } });
    await expandEvidenceDetail();
    await screen.findByText('192.0.2.1:8211');
    fireEvent.change(screen.getByRole('textbox', { name: /搜索已知玩家/i }), { target: { value: 'Morgan' } });
    expect(select).toHaveValue('u/1');
    expect(within(select).getByRole('option', { name: /Avery/ })).toBeInTheDocument();
    expect(screen.getByText(/IP · 管理员私有/i)).toBeInTheDocument();
  });

  it('warns when any evidence source reaches the response limit', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      private_samples: Array.from({ length: 500 }, (_, index) => ({ user_id: 'u/1', observed_at: new Date(Date.UTC(2026, 6, 13, 8, 0, index)).toISOString(), ip: `192.0.2.${index}`, ping: 10, level: 2, source_ref: `private-${index}` })),
    });
    render(<PlayerTimeline includePrivate players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await expandEvidenceDetail();
    expect(await screen.findByText(/默认加载时间范围内/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /加载更早/i })).toBeInTheDocument();
  });

  it('shows loading, not-found, and request failure states', async () => {
    const request = deferred<PlayerTimelineResponse>();
    vi.mocked(api.getPlayerTimeline).mockReturnValueOnce(request.promise);
    const { rerender } = render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    // List skeleton is deferred; loading is still visible on map + detail toggle.
    expect(await screen.findByText(/正在加载轨迹证据/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /证据明细/i })).toHaveTextContent(/加载中/);
    request.reject(new api.ApiError('player not found', 404));
    expect(await screen.findByRole('alert')).toHaveTextContent(/该玩家已不在观察记录中/i);
    vi.mocked(api.getPlayerTimeline).mockRejectedValueOnce(new Error('database offline'));
    rerender(<PlayerTimeline players={players} refreshKey={1} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('database offline');
  });

  it('blocks inverted and over-31-day ranges before any request', async () => {
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByLabelText(/开始/i), { target: { value: '2026-05-01T00:00' } });
    fireEvent.change(screen.getByLabelText(/结束/i), { target: { value: '2026-07-13T00:00' } });
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    expect(await screen.findByRole('alert')).toHaveTextContent(/31 天/i);
    expect(api.getPlayerTimeline).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText(/开始/i), { target: { value: '2026-07-14T00:00' } });
    expect(await screen.findByRole('alert')).toHaveTextContent(/晚于开始时间/i);
    expect(api.getPlayerTimeline).not.toHaveBeenCalled();
  });

  it('applies range presets without manual datetime typing', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date('2026-07-14T12:00:00'));
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.click(screen.getByRole('button', { name: /最近 1 小时/i }));
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(api.getPlayerTimeline).toHaveBeenCalled());
    const [, startIso, endIso] = vi.mocked(api.getPlayerTimeline).mock.calls.at(-1)!;
    const start = Date.parse(startIso as string);
    const end = Date.parse(endIso as string);
    expect(end - start).toBe(60 * 60 * 1000);
  });

  it('advances cursor while playing and stops at the end without forcing list scroll', async () => {
    const payload: PlayerTimelineResponse = {
      user_id: 'u/1',
      events: [
        { id: 'e1', event_type: 'player_joined', occurred_at: '2026-07-13T08:00:00Z', observed_at: '2026-07-13T08:00:00Z', source: 'guard', confidence: 'observed', summary: 'a' },
        { id: 'e2', event_type: 'player_left', occurred_at: '2026-07-13T09:00:00Z', observed_at: '2026-07-13T09:00:00Z', source: 'guard', confidence: 'observed', summary: 'b' },
        { id: 'e3', event_type: 'player_joined', occurred_at: '2026-07-13T10:00:00Z', observed_at: '2026-07-13T10:00:00Z', source: 'guard', confidence: 'observed', summary: 'c' },
      ],
      trajectories: [],
      private_samples: [],
    };
    vi.mocked(api.getPlayerTimeline).mockResolvedValue(payload);
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(screen.getByLabelText(/时间轴光标/)).not.toBeDisabled());
    // Collapsed by default so playback does not drag a long list into view.
    expect(screen.queryByTestId('timeline-spine-window')).not.toBeInTheDocument();
    scrollIntoView.mockClear();
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole('button', { name: /播放/i }));
    expect(screen.getByRole('button', { name: /暂停/i })).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(800);
    });
    expect(screen.getByLabelText(/时间轴光标/)).toHaveValue('1');
    // Still playing: list must not steal the page viewport from the map.
    expect(screen.getByRole('button', { name: /暂停/i })).toBeInTheDocument();
    expect(scrollIntoView).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(800);
    });
    expect(screen.getByLabelText(/时间轴光标/)).toHaveValue('2');
    // Final step lands on last index and auto-pauses (timeout chain may need a flush).
    await act(async () => {
      await vi.advanceTimersByTimeAsync(800);
    });
    expect(screen.getByRole('button', { name: /播放/i })).toBeInTheDocument();
  });

  it('exposes play mode and map layer toggles', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      events: [
        { id: 'e1', event_type: 'player_joined', occurred_at: '2026-07-13T08:00:00Z', observed_at: '2026-07-13T08:00:00Z', source: 'guard', confidence: 'observed', summary: 'a' },
        { id: 'e2', event_type: 'player_left', occurred_at: '2026-07-13T09:00:00Z', observed_at: '2026-07-13T09:00:00Z', source: 'guard', confidence: 'observed', summary: 'b' },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(screen.getByLabelText(/播放模式/i)).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText(/播放模式/i), { target: { value: 'time' } });
    expect(screen.getByLabelText(/播放模式/i)).toHaveValue('time');

    const points = screen.getByRole('button', { name: /^点$/i });
    const lines = screen.getByRole('button', { name: /^线$/i });
    const landmark = screen.getByRole('button', { name: /^地标$/i });
    expect(points).toHaveAttribute('aria-pressed', 'true');
    expect(lines).toHaveAttribute('aria-pressed', 'true');
    expect(landmark).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(points);
    expect(points).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(lines);
    expect(lines).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(landmark);
    expect(landmark).toHaveAttribute('aria-pressed', 'true');
  });

  it('virtualizes a long timeline list to a window of rows', async () => {
    const events = Array.from({ length: 80 }, (_, index) => ({
      id: `e${index}`,
      event_type: 'player_joined' as const,
      occurred_at: new Date(Date.UTC(2026, 6, 13, 8, 0, index)).toISOString(),
      observed_at: new Date(Date.UTC(2026, 6, 13, 8, 0, index)).toISOString(),
      source: 'guard' as const,
      confidence: 'observed' as const,
      summary: `row-${index}`,
    }));
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({ ...empty, events });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await expandEvidenceDetail();
    await waitFor(() => expect(screen.getByTestId('timeline-spine-window')).toBeInTheDocument());
    const focusButtons = screen.getAllByRole('button', { name: /将回放光标移动到第/i });
    // Only a window of rows is mounted — far less than 80.
    expect(focusButtons.length).toBeGreaterThan(0);
    expect(focusButtons.length).toBeLessThan(80);
  });

  it('toggles delay color mode legend', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      trajectories: [
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T08:00:00Z', x: 100, y: 200, ping: 20, level: 1, source_ref: 'one', runtime_epoch: 0 },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await screen.findByTestId('timeline-map');
    expect(screen.queryByLabelText(/延迟图例/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /^延迟$/i }));
    expect(screen.getByLabelText(/延迟图例/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /^位置$/i }));
    expect(screen.queryByLabelText(/延迟图例/i)).not.toBeInTheDocument();
  });

  it('shows 靠近 landmark label for sample on known coordinates', async () => {
    const landmark = MAP_LANDMARKS[0]!;
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      trajectories: [
        {
          user_id: 'u/1',
          segment_id: 's1',
          observed_at: '2026-07-13T08:00:00Z',
          x: landmark.x,
          y: landmark.y,
          ping: 20,
          level: 1,
          source_ref: 'one',
          runtime_epoch: 0,
        },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(screen.getAllByText(/靠近/).length).toBeGreaterThan(0));
  });

  it('seeks the cursor when a trajectory sample is focused via seek path', async () => {
    // Map marker clicks are Leaflet-internal; cover the same seek mapping used by O10.
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      events: [
        { id: 'e1', event_type: 'player_joined', occurred_at: '2026-07-13T07:00:00Z', observed_at: '2026-07-13T07:00:00Z', source: 'guard', confidence: 'observed', summary: 'join' },
      ],
      trajectories: [
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T08:00:00Z', x: 10, y: 20, ping: 20, level: 1, source_ref: 'a', runtime_epoch: 0 },
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T09:00:00Z', x: 30, y: 40, ping: 40, level: 2, source_ref: 'b', runtime_epoch: 0 },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(screen.getByLabelText(/时间轴光标/)).not.toBeDisabled());
    expect(screen.getByLabelText(/时间轴光标/)).toHaveValue('0');
    await expandEvidenceDetail();
    // Jump to second trajectory row via list crosshair (same index resolution as map seek)
    const focusButtons = screen.getAllByRole('button', { name: /将回放光标移动到第/i });
    fireEvent.click(focusButtons[2]!); // event + traj0 + traj1 → index 2
    expect(screen.getByLabelText(/时间轴光标/)).toHaveValue('2');
  });

  it('keeps evidence detail collapsed until expanded', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      events: [
        { id: 'e1', event_type: 'player_joined', occurred_at: '2026-07-13T08:00:00Z', observed_at: '2026-07-13T08:00:00Z', source: 'guard', confidence: 'observed', summary: 'a' },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    const toggle = await screen.findByRole('button', { name: /证据明细/i });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByTestId('timeline-spine-window')).not.toBeInTheDocument();
    expect(screen.queryByText('玩家加入')).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(await screen.findByTestId('timeline-spine-window')).toBeInTheDocument();
    expect(screen.getByText('玩家加入')).toBeInTheDocument();
  });

  it('steps the cursor with 下一步 and 上一步', async () => {
    const payload: PlayerTimelineResponse = {
      user_id: 'u/1',
      events: [
        { id: 'e1', event_type: 'player_joined', occurred_at: '2026-07-13T08:00:00Z', observed_at: '2026-07-13T08:00:00Z', source: 'guard', confidence: 'observed', summary: 'a' },
        { id: 'e2', event_type: 'player_left', occurred_at: '2026-07-13T09:00:00Z', observed_at: '2026-07-13T09:00:00Z', source: 'guard', confidence: 'observed', summary: 'b' },
        { id: 'e3', event_type: 'player_joined', occurred_at: '2026-07-13T10:00:00Z', observed_at: '2026-07-13T10:00:00Z', source: 'guard', confidence: 'observed', summary: 'c' },
      ],
      trajectories: [],
      private_samples: [],
    };
    vi.mocked(api.getPlayerTimeline).mockResolvedValue(payload);
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /玩家/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(screen.getByLabelText(/时间轴光标/)).not.toBeDisabled());
    expect(screen.getByLabelText(/时间轴光标/)).toHaveValue('0');
    fireEvent.click(screen.getByRole('button', { name: /下一步/i }));
    expect(screen.getByLabelText(/时间轴光标/)).toHaveValue('1');
    fireEvent.click(screen.getByRole('button', { name: /上一步/i }));
    expect(screen.getByLabelText(/时间轴光标/)).toHaveValue('0');
  });
});
