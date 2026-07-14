import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import { LiveMap } from './LiveMap';

vi.mock('../api', async (load) => ({
  ...(await load<typeof import('../api')>()),
  getLivePositions: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getLivePositions).mockResolvedValue({
    as_of: '2026-07-14T12:00:00Z',
    online_count: 2,
    positioned: 1,
    players: [
      {
        user_id: 'u/1',
        name: 'Avery',
        x: 1000,
        y: -2000,
        ping: 22,
        level: 12,
      },
    ],
  });
});

describe('LiveMap', () => {
  it('loads live positions and lists players', async () => {
    render(<LiveMap />);
    expect(await screen.findByTestId('live-map')).toBeInTheDocument();
    expect(await screen.findByText('Avery')).toBeInTheDocument();
    expect(screen.getByText(/1\/2 有坐标/)).toBeInTheDocument();
    expect(api.getLivePositions).toHaveBeenCalled();
  });

  it('opens player timeline from selection', async () => {
    const user = userEvent.setup();
    const onOpenPlayer = vi.fn();
    render(<LiveMap onOpenPlayer={onOpenPlayer} />);
    await screen.findByText('Avery');
    await user.click(screen.getByRole('button', { name: /Avery/i }));
    await user.click(screen.getByRole('button', { name: /打开轨迹时间轴/i }));
    expect(onOpenPlayer).toHaveBeenCalledWith('u/1');
  });

  it('shows empty state when no positioned players', async () => {
    vi.mocked(api.getLivePositions).mockResolvedValue({
      as_of: '2026-07-14T12:00:00Z',
      online_count: 0,
      positioned: 0,
      players: [],
    });
    render(<LiveMap />);
    await waitFor(() => {
      expect(screen.getByText(/没有可显示坐标的在线玩家/)).toBeInTheDocument();
    });
  });
});
