import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import type { Player, PlayerTimelineResponse } from '../api';
import { PlayerTimeline } from './PlayerTimeline';

vi.mock('../api', async (load) => ({
  ...(await load<typeof import('../api')>()),
  getPlayerTimeline: vi.fn(),
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

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getPlayerTimeline).mockResolvedValue(empty);
});
afterEach(() => vi.clearAllMocks());

describe('PlayerTimeline', () => {
  it('starts intentionally empty and offers accessible player and range controls', () => {
    render(<PlayerTimeline players={players} refreshKey={0} />);
    expect(screen.getByText(/select a player to open/i)).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: /player/i })).toHaveValue('');
    expect(screen.getByLabelText(/start/i)).toHaveAttribute('type', 'datetime-local');
    expect(screen.getByLabelText(/end/i)).toHaveAttribute('type', 'datetime-local');
    expect(api.getPlayerTimeline).not.toHaveBeenCalled();
  });

  it('selects a known player and requests its RFC3339 timeline', async () => {
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /player/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(api.getPlayerTimeline).toHaveBeenCalledTimes(1));
    expect(api.getPlayerTimeline).toHaveBeenCalledWith('u/1', expect.stringMatching(/T.*(?:Z|[+-]\d\d:\d\d)$/), expect.stringMatching(/T.*(?:Z|[+-]\d\d:\d\d)$/), 500, expect.any(AbortSignal));
    expect(await screen.findByText(/no observations recorded/i)).toBeInTheDocument();
  });

  it('aborts replaced work and ignores stale responses', async () => {
    const first = deferred<PlayerTimelineResponse>();
    const second = deferred<PlayerTimelineResponse>();
    vi.mocked(api.getPlayerTimeline).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /player/i }), { target: { value: 'u/1' } });
    await waitFor(() => expect(api.getPlayerTimeline).toHaveBeenCalledTimes(1));
    const firstSignal = vi.mocked(api.getPlayerTimeline).mock.calls[0]?.[4];
    fireEvent.change(screen.getByRole('combobox', { name: /player/i }), { target: { value: 'u2' } });
    expect(firstSignal?.aborted).toBe(true);
    second.resolve({ ...empty, user_id: 'u2' });
    await screen.findByText(/no observations recorded/i);
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
        { user_id: 'u/1', segment_id: 's2', observed_at: '2026-07-13T09:00:00Z', x: 123.45, y: -9.5, ping: 20, level: 12, source_ref: 'poll-2' },
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T08:30:00Z', x: 10, y: 20, ping: 18, level: 12, source_ref: 'poll-1' },
      ],
      private_samples: [{ user_id: 'u/1', observed_at: '2026-07-13T08:45:00Z', ip: '2001:db8::1:8211', ping: 17, level: 12, building_count: 3, source_ref: 'private-1' }],
    };
    const original = JSON.stringify(payload);
    vi.mocked(api.getPlayerTimeline).mockResolvedValue(payload);
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /player/i }), { target: { value: 'u/1' } });

    expect(await screen.findByText('Avery joined')).toBeInTheDocument();
    const items = screen.getAllByRole('listitem').map((node) => node.textContent);
    expect(items.join('|')).toMatch(/Avery joined.*10.*20.*2001:db8::1:8211.*123.45.*-9.5.*Unsupported event/i);
    expect(screen.getByText(/IP · Admin private/i)).toBeInTheDocument();
    expect(screen.getAllByText(/Coordinates · Admin private/i)).toHaveLength(2);
    expect(screen.getAllByText(/REST/i).length).toBeGreaterThan(0);
    expect(screen.getByText('Snapshot')).toBeInTheDocument();
    expect(screen.getAllByText(/Occurred/i).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Observed/i).length).toBeGreaterThan(0);
    const segment = screen.getByText(/New observation segment/i).closest('li');
    expect(segment?.previousElementSibling).toHaveTextContent('2001:db8::1:8211');
    expect(segment?.nextElementSibling).toHaveTextContent('123.45, -9.5');
    const privateEntry = screen.getByText(/IP · Admin private/i).closest('li');
    expect(privateEntry).not.toBeNull();
    expect(within(privateEntry!).getByText('REST')).toBeInTheDocument();
    expect(within(privateEntry!).queryByText('Guard')).not.toBeInTheDocument();
    expect(JSON.stringify(payload)).toBe(original);
    expect(screen.queryByText('future_event')).not.toBeInTheDocument();
  });

  it('places a trajectory evidence-gap marker before the next trajectory despite an intervening event', async () => {
    vi.mocked(api.getPlayerTimeline).mockResolvedValue({
      ...empty,
      events: [{ id: 'between', event_type: 'player_joined', occurred_at: '2026-07-13T08:59:00Z', observed_at: '2026-07-13T08:59:00Z', source: 'guard', confidence: 'observed', summary: 'joined' }],
      trajectories: [
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T08:00:00Z', x: 1, y: 1, ping: 20, level: 1, source_ref: 'one' },
        { user_id: 'u/1', segment_id: 's1', observed_at: '2026-07-13T09:00:00Z', x: 2, y: 2, ping: 20, level: 1, source_ref: 'two' },
      ],
    });
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /player/i }), { target: { value: 'u/1' } });
    const gap = await screen.findByText(/Observation gap/i);
    expect(gap.closest('li')?.nextElementSibling).toHaveTextContent('2, 2');
  });

  it('shows loading, not-found, and request failure states', async () => {
    const request = deferred<PlayerTimelineResponse>();
    vi.mocked(api.getPlayerTimeline).mockReturnValueOnce(request.promise);
    const { rerender } = render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByRole('combobox', { name: /player/i }), { target: { value: 'u/1' } });
    expect(await screen.findByRole('status', { name: /loading timeline/i })).toBeInTheDocument();
    request.reject(new api.ApiError('player not found', 404));
    expect(await screen.findByRole('alert')).toHaveTextContent(/player is no longer known/i);
    vi.mocked(api.getPlayerTimeline).mockRejectedValueOnce(new Error('database offline'));
    rerender(<PlayerTimeline players={players} refreshKey={1} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('database offline');
  });

  it('blocks inverted and over-31-day ranges before any request', async () => {
    render(<PlayerTimeline players={players} refreshKey={0} />);
    fireEvent.change(screen.getByLabelText(/start/i), { target: { value: '2026-05-01T00:00' } });
    fireEvent.change(screen.getByLabelText(/end/i), { target: { value: '2026-07-13T00:00' } });
    fireEvent.change(screen.getByRole('combobox', { name: /player/i }), { target: { value: 'u/1' } });
    expect(await screen.findByRole('alert')).toHaveTextContent(/31 days/i);
    expect(api.getPlayerTimeline).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText(/start/i), { target: { value: '2026-07-14T00:00' } });
    expect(await screen.findByRole('alert')).toHaveTextContent(/after start/i);
    expect(api.getPlayerTimeline).not.toHaveBeenCalled();
  });
});
