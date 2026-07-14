import { afterEach, describe, expect, it, vi } from 'vitest';
import { getAnalyticsActivity, getAnalyticsSummary, getLivePositions, getPlayerTimeline, getPlayerWorldPOIs } from './api';
import type { AnalyticsActivity, AnalyticsSummary, PlayerTimelineResponse, PlayerWorldPOIsResponse } from './api';

const summaryFixture = {
  online_count: 1, as_of: null, today_observed_ms: 60_000, peak_count: 2, peak_at: null,
  active_players: 1, ranking_period: 'week',
  ranking: [{ user_id: 'one', name: 'One', observed_ms: 60_000, online: true }],
} satisfies AnalyticsSummary;

const activityFixture = {
  range: '30d', timezone: 'Asia/Shanghai', start: '2026-06-12', end: '2026-07-12',
  concurrency: [{ at: '2026-07-11T00:00:00Z', average_count: null, max_count: null, coverage: 0 }],
  player: null,
} satisfies AnalyticsActivity;

afterEach(() => vi.restoreAllMocks());

describe('analytics API', () => {
  it('requests the selected ranking period and parses the response', async () => {
    const payload = summaryFixture;
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(payload)));

    await expect(getAnalyticsSummary('week')).resolves.toEqual(payload);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/analytics/summary?ranking=week', expect.objectContaining({ credentials: 'same-origin' }));
  });

  it('encodes the optional player identifier in activity requests', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(activityFixture)));

    await getAnalyticsActivity('30d', 'steam id&one');
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/analytics/activity?range=30d&user_id=steam+id%26one');
  });

  it('can omit concurrency for focused player activity requests', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(activityFixture)));
    const controller = new AbortController();
    await getAnalyticsActivity('7d', 'u1', controller.signal, false);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/analytics/activity?range=7d&user_id=u1&include_concurrency=false');
    expect(fetchMock.mock.calls[0]?.[1]).toEqual(expect.objectContaining({ signal: controller.signal }));
    controller.abort();
    expect(controller.signal.aborted).toBe(true);
  });

  it('preserves existing API error behavior', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ error: { message: 'bad range' } }), { status: 400 }));
    await expect(getAnalyticsActivity('7d')).rejects.toMatchObject({ message: 'bad range', status: 400 });
  });
});

describe('timeline API', () => {
  it('uses the public timeline endpoint by default', async () => {
    const payload = { user_id: 'steam/id one', events: [], trajectories: [], private_samples: [] } satisfies PlayerTimelineResponse;
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(payload)));
    const controller = new AbortController();

    await expect(getPlayerTimeline('steam/id one', '2026-07-12T08:00:00+08:00', '2026-07-13T08:00:00+08:00', 500, controller.signal)).resolves.toEqual(payload);

    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/players/steam%2Fid%20one/timeline?start=2026-07-12T08%3A00%3A00%2B08%3A00&end=2026-07-13T08%3A00%3A00%2B08%3A00&limit=500');
    expect(fetchMock.mock.calls[0]?.[1]).toEqual(expect.objectContaining({ signal: controller.signal }));
  });

  it('uses the private administrator timeline endpoint when requested', async () => {
    const payload = { user_id: 'steam/id one', events: [], trajectories: [], private_samples: [] } satisfies PlayerTimelineResponse;
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(payload)));

    await getPlayerTimeline('steam/id one', '2026-07-12T08:00:00Z', '2026-07-13T08:00:00Z', 500, undefined, true);

    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/admin/players/steam%2Fid%20one/timeline?start=2026-07-12T08%3A00%3A00Z&end=2026-07-13T08%3A00%3A00Z&limit=500');
  });

  it('requests public world POIs for a player', async () => {
    const payload = {
      user_id: 'steam/id one',
      source: 'save_import',
      as_of: '2026-07-14T00:00:00Z',
      pois: [{ id: 'guild:1:base:2', name_zh: 'Base', kind: 'guild_base', x: 1, y: 2, guild_name: 'G' }],
    } satisfies PlayerWorldPOIsResponse;
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(payload)));
    const controller = new AbortController();

    await expect(getPlayerWorldPOIs('steam/id one', controller.signal)).resolves.toEqual(payload);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/players/steam%2Fid%20one/world-pois');
    expect(fetchMock.mock.calls[0]?.[1]).toEqual(expect.objectContaining({ signal: controller.signal }));
  });

  it('requests live positions for the world map', async () => {
    const payload = {
      as_of: '2026-07-14T12:00:00Z',
      online_count: 1,
      positioned: 1,
      players: [{ user_id: 'u1', name: 'A', x: 1, y: 2, ping: 10, level: 3 }],
    };
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(payload)));
    await expect(getLivePositions()).resolves.toEqual(payload);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/live/positions');
  });
});
