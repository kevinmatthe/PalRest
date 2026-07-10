import { afterEach, describe, expect, it, vi } from 'vitest';
import { getAnalyticsActivity, getAnalyticsSummary } from './api';
import type { AnalyticsActivity, AnalyticsSummary } from './api';

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

  it('preserves existing API error behavior', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ error: { message: 'bad range' } }), { status: 400 }));
    await expect(getAnalyticsActivity('7d')).rejects.toMatchObject({ message: 'bad range', status: 400 });
  });
});
