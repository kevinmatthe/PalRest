import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from './api';
import { App } from './main';

vi.mock('./api', async (load) => {
  const actual = await load<typeof import('./api')>();
  return {
    ...actual,
    getHealth: vi.fn(), getStatus: vi.fn(), getPlayers: vi.fn(), getPolicies: vi.fn(), getAdminSession: vi.fn(),
    loginAdmin: vi.fn(), logoutAdmin: vi.fn(), resetPlayer: vi.fn(), savePolicies: vi.fn(),
  };
});
vi.mock('./components/AnalyticsDashboard', () => ({
  AnalyticsDashboard: ({ refreshKey }: { refreshKey: number }) => <div>Analytics dashboard token {refreshKey}</div>,
}));
vi.mock('./components/PolicyManager', () => ({
  PolicyManager: ({ onBack }: { onBack: () => void }) => <section><h2>Policy manager</h2><button onClick={onBack}>Back to dashboard</button></section>,
}));

const admin = { enabled: true, authenticated: true, passkey: false };
const policies = {
  version: 1, source: 'database' as const, timezone: 'Asia/Shanghai', overrides: {},
  default: { enabled: true, strategy: 'fixed', period: 'daily', reset_at: '00:00', limit_ms: 3_600_000, warning_before_ms: [] },
};

beforeEach(() => {
  vi.mocked(api.getHealth).mockResolvedValue({ status: 'healthy', sqlite: 'available' });
  vi.mocked(api.getStatus).mockResolvedValue({ started_at: '2026-07-11T00:00:00Z', online_count: 0, config_version: 1 });
  vi.mocked(api.getPlayers).mockResolvedValue({ players: [] });
  vi.mocked(api.getPolicies).mockResolvedValue(policies);
  vi.mocked(api.getAdminSession).mockResolvedValue(admin);
  vi.mocked(api.logoutAdmin).mockResolvedValue({ ...admin, authenticated: false });
});

afterEach(() => vi.useRealTimers());

describe('App analytics navigation and refresh ownership', () => {
  it('switches Overview and Analytics with aria-current and policy back returns to Overview', async () => {
    render(<App />);
    const overview = await screen.findByRole('button', { name: 'Overview' });
    const analytics = screen.getByRole('button', { name: 'Analytics' });
    expect(overview).toHaveAttribute('aria-current', 'page');
    fireEvent.click(analytics);
    expect(analytics).toHaveAttribute('aria-current', 'page');
    expect(overview).not.toHaveAttribute('aria-current');
    expect(screen.getByRole('button', { name: 'Manage policy' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Manage policy' }));
    expect(screen.getByRole('heading', { name: 'Policy manager' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Back to dashboard' }));
    expect(screen.getByRole('button', { name: 'Overview' })).toHaveAttribute('aria-current', 'page');
  });

  it('logout from Analytics returns to Overview', async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole('button', { name: 'Analytics' }));
    fireEvent.click(screen.getByRole('button', { name: 'Admin' }));
    await waitFor(() => expect(api.logoutAdmin).toHaveBeenCalledTimes(1));
    expect(screen.getByRole('button', { name: 'Overview' })).toHaveAttribute('aria-current', 'page');
  });

  it('manual refresh updates analytics immediately and one cadence tick refreshes common data and analytics', async () => {
    vi.useFakeTimers();
    render(<App />);
    await act(async () => { await Promise.resolve(); });
    fireEvent.click(screen.getByRole('button', { name: 'Analytics' }));
    expect(screen.getByText('Analytics dashboard token 0')).toBeInTheDocument();
    fireEvent.click(screen.getByTitle('Refresh now'));
    expect(screen.getByText('Analytics dashboard token 1')).toBeInTheDocument();
    await act(async () => { await Promise.resolve(); });
    const callsAfterManual = vi.mocked(api.getHealth).mock.calls.length;
    await act(async () => { vi.advanceTimersByTime(10_000); await Promise.resolve(); });
    expect(api.getHealth).toHaveBeenCalledTimes(callsAfterManual + 1);
    expect(screen.getByText('Analytics dashboard token 2')).toBeInTheDocument();
  });

  it('aborts the prior common-data request when manual refresh starts', async () => {
    let firstSignal: AbortSignal | undefined;
    vi.mocked(api.getHealth).mockImplementationOnce((signal) => {
      firstSignal = signal;
      return new Promise(() => undefined);
    });
    render(<App />);
    expect(firstSignal?.aborted).toBe(false);
    fireEvent.click(screen.getByTitle('Refresh now'));
    expect(firstSignal?.aborted).toBe(true);
  });
});
