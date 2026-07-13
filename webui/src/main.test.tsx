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
vi.mock('./components/PlayerTimeline', () => ({
  PlayerTimeline: ({ includePrivate, refreshKey }: { includePrivate?: boolean; refreshKey: number }) => <div>Player timeline token {refreshKey} private {String(Boolean(includePrivate))}</div>,
}));

const admin = { enabled: true, authenticated: true, passkey: false };
const policies = {
  version: 1, source: 'database' as const, timezone: 'Asia/Shanghai', overrides: {},
  default: { enabled: true, strategy: 'fixed', period: 'daily', reset_at: '00:00', limit_ms: 3_600_000, warning_before_ms: [] },
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => { resolve = res; });
  return { promise, resolve };
}

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

  it('advances the analytics cadence even when the common refresh fails', async () => {
    vi.useFakeTimers();
    render(<App />);
    await act(async () => { await Promise.resolve(); });
    fireEvent.click(screen.getByRole('button', { name: 'Analytics' }));
    vi.mocked(api.getHealth).mockRejectedValueOnce(new Error('common unavailable'));
    await act(async () => { vi.advanceTimersByTime(10_000); await Promise.resolve(); });
    expect(screen.getByText('Analytics dashboard token 1')).toBeInTheDocument();
    expect(screen.getByText('common unavailable')).toBeInTheDocument();
  });
});

describe('public timeline navigation', () => {
  it('exposes Timeline publicly and enables private mode only for an authenticated administrator', async () => {
    const { unmount } = render(<App />);
    const timeline = await screen.findByRole('button', { name: '时间轴' });
    expect(screen.queryByText(/Player timeline token/)).not.toBeInTheDocument();
    fireEvent.click(timeline);
    expect(timeline).toHaveAttribute('aria-current', 'page');
    expect(screen.getByText(/Player timeline token/)).toHaveTextContent('private true');
    unmount();

    vi.mocked(api.getAdminSession).mockResolvedValueOnce({ ...admin, authenticated: false });
    render(<App />);
    const publicTimeline = await screen.findByRole('button', { name: '时间轴' });
    fireEvent.click(publicTimeline);
    expect(screen.getByText(/Player timeline token/)).toHaveTextContent('private false');
  });

  it('keeps public Timeline open if a refreshed session loses authentication', async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole('button', { name: '时间轴' }));
    expect(screen.getByText(/Player timeline token/)).toBeInTheDocument();
    vi.mocked(api.getAdminSession).mockResolvedValueOnce({ ...admin, authenticated: false });
    fireEvent.click(screen.getByTitle('Refresh now'));
    await waitFor(() => expect(screen.getByRole('button', { name: '时间轴' })).toHaveAttribute('aria-current', 'page'));
    expect(screen.getByText(/Player timeline token/)).toHaveTextContent('private false');
  });

  it('keeps the existing private timeline visible until session refresh resolves', async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole('button', { name: '时间轴' }));
    const current = screen.getByText(/Player timeline token/).textContent;
    const session = deferred<api.AdminSession>();
    vi.mocked(api.getAdminSession).mockReturnValueOnce(session.promise);
    fireEvent.click(screen.getByTitle('Refresh now'));
    expect(screen.getByText(/Player timeline token/)).toHaveTextContent('private true');
    session.resolve({ ...admin, authenticated: false });
    await waitFor(() => expect(screen.getByText(/Player timeline token/)).toHaveTextContent('private false'));
  });

  it('downgrades to public timeline when auth is lost even if another common request fails', async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole('button', { name: '时间轴' }));
    expect(screen.getByText(/Player timeline token/)).toBeInTheDocument();
    vi.mocked(api.getAdminSession).mockResolvedValueOnce({ ...admin, authenticated: false });
    vi.mocked(api.getHealth).mockRejectedValueOnce(new Error('health offline'));
    fireEvent.click(screen.getByTitle('Refresh now'));
    await waitFor(() => expect(screen.getByText(/Player timeline token/)).toHaveTextContent('private false'));
    expect(screen.getByRole('button', { name: '时间轴' })).toBeInTheDocument();
    expect(screen.getByText('health offline')).toBeInTheDocument();
  });
});
