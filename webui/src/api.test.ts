import { afterEach, describe, expect, it, vi } from 'vitest';
import { getAnalyticsActivity, getAnalyticsSummary } from './api';

afterEach(() => vi.restoreAllMocks());

describe('analytics API', () => {
  it('requests the selected ranking period and parses the response', async () => {
    const payload = { ranking_period: 'week', ranking: [] };
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(payload)));

    await expect(getAnalyticsSummary('week')).resolves.toEqual(payload);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/analytics/summary?ranking=week', expect.objectContaining({ credentials: 'same-origin' }));
  });

  it('encodes the optional player identifier in activity requests', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}'));

    await getAnalyticsActivity('30d', 'steam id&one');
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/analytics/activity?range=30d&user_id=steam+id%26one');
  });

  it('preserves existing API error behavior', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ error: { message: 'bad range' } }), { status: 400 }));
    await expect(getAnalyticsActivity('7d')).rejects.toMatchObject({ message: 'bad range', status: 400 });
  });
});
