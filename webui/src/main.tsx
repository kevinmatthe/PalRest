import { StrictMode, useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  AlertTriangle,
  CheckCircle2,
  CircleGauge,
  Clock3,
  Database,
  RefreshCw,
  Search,
  Shield,
  Users,
  WifiOff,
} from 'lucide-react';
import {
  getHealth,
  getPlayers,
  getPolicies,
  getStatus,
  type HealthStatus,
  type Player,
  type Policies,
  type PollStatus,
} from './api';
import { formatDateTime, formatDuration, formatExactDateTime, percent, titleCase } from './utils';
import './styles.css';

type DashboardData = {
  health: HealthStatus;
  status: PollStatus;
  players: Player[];
  policies: Policies;
};

type LoadState =
  | { kind: 'loading'; data?: DashboardData; error?: undefined }
  | { kind: 'ready'; data: DashboardData; error?: undefined }
  | { kind: 'error'; data?: DashboardData; error: string };

const refreshIntervalMS = 10_000;

function App() {
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  const [query, setQuery] = useState('');
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null);
  const [manualRefreshKey, setManualRefreshKey] = useState(0);

  useEffect(() => {
    let mounted = true;
    const controller = new AbortController();

    async function load() {
      setState((current) => ({ kind: 'loading', data: current.data }));
      try {
        const [health, status, playersResponse, policies] = await Promise.all([
          getHealth(controller.signal),
          getStatus(controller.signal),
          getPlayers(controller.signal),
          getPolicies(controller.signal),
        ]);
        if (!mounted) {
          return;
        }
        setState({ kind: 'ready', data: { health, status, players: playersResponse.players, policies } });
        setLastRefresh(new Date());
      } catch (error) {
        if (!mounted || controller.signal.aborted) {
          return;
        }
        const message = error instanceof Error ? error.message : 'Could not load dashboard data';
        setState((current) => ({ kind: 'error', data: current.data, error: message }));
      }
    }

    load();
    const timer = window.setInterval(load, refreshIntervalMS);
    return () => {
      mounted = false;
      controller.abort();
      window.clearInterval(timer);
    };
  }, [manualRefreshKey]);

  const data = state.data;
  const players = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    const source = data?.players ?? [];
    if (!normalized) {
      return source;
    }
    return source.filter((player) =>
      [player.name, player.account_name, player.user_id, player.player_id].some((value) =>
        value.toLowerCase().includes(normalized),
      ),
    );
  }, [data?.players, query]);

  const activePlayers = data?.players.filter((player) => player.online).length ?? 0;
  const atRiskPlayers =
    data?.players.filter((player) => player.enabled && !player.exempt && player.remaining_ms <= 10 * 60 * 1000).length ?? 0;
  const enforcedPlayers = data?.players.filter((player) => player.enforcement_state).length ?? 0;
  const defaultRule = data?.policies.default;
  const overrides = Object.entries(data?.policies.overrides ?? {});
  const serviceState = data ? resolveServiceState(data.health, data.status) : 'loading';

  return (
    <main className="app-shell">
      <header className="topbar">
        <div>
          <p className="eyebrow">PalRest Console</p>
          <h1>Palworld playtime control</h1>
        </div>
        <div className="topbar-actions">
          <StatusPill state={serviceState} />
          <button className="icon-button" type="button" onClick={() => setManualRefreshKey((value) => value + 1)} title="Refresh now">
            <RefreshCw size={18} />
          </button>
        </div>
      </header>

      {state.kind === 'error' && (
        <section className="notice" role="status">
          <AlertTriangle size={18} />
          <span>{state.error}</span>
        </section>
      )}

      <section className="status-grid" aria-label="Service status">
        <MetricCard icon={<Users size={20} />} label="Online players" value={activePlayers.toString()} detail={`API reports ${data?.status.online_count ?? 0}`} />
        <MetricCard icon={<CircleGauge size={20} />} label="Near limit" value={atRiskPlayers.toString()} detail="10 minutes or less" tone={atRiskPlayers > 0 ? 'warn' : 'ok'} />
        <MetricCard icon={<Shield size={20} />} label="Enforced" value={enforcedPlayers.toString()} detail="Current period state" tone={enforcedPlayers > 0 ? 'warn' : 'neutral'} />
        <MetricCard
          icon={<Database size={20} />}
          label="SQLite"
          value={titleCase(data?.health.sqlite)}
          detail={`Last poll ${formatDateTime(data?.status.last_success)}`}
          tone={data?.health.sqlite === 'available' ? 'ok' : 'warn'}
        />
      </section>

      <section className="main-grid">
        <section className="panel players-panel">
          <div className="panel-header">
            <div>
              <h2>Players</h2>
              <p>{state.kind === 'loading' && !data ? 'Loading current sessions' : `${players.length} shown from ${data?.players.length ?? 0} online snapshots`}</p>
            </div>
            <label className="search-box">
              <Search size={16} />
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search players" />
            </label>
          </div>
          <PlayerTable players={players} loading={state.kind === 'loading' && !data} />
        </section>

        <aside className="side-stack">
          <section className="panel">
            <div className="panel-header compact">
              <div>
                <h2>Policy</h2>
                <p>Default rule</p>
              </div>
            </div>
            {defaultRule ? (
              <div className="policy-summary">
                <div className="policy-state">
                  <span className={`dot ${defaultRule.enabled ? 'ok' : 'muted'}`} />
                  <strong>{defaultRule.enabled ? 'Enabled' : 'Disabled'}</strong>
                </div>
                <dl>
                  <div>
                    <dt>Limit</dt>
                    <dd>{formatDuration(defaultRule.limit_ms)}</dd>
                  </div>
                  <div>
                    <dt>Period</dt>
                    <dd>{titleCase(defaultRule.period)}</dd>
                  </div>
                  <div>
                    <dt>Reset</dt>
                    <dd>{defaultRule.reset_weekday ? `${defaultRule.reset_weekday} ` : ''}{defaultRule.reset_at}</dd>
                  </div>
                  <div>
                    <dt>Timezone</dt>
                    <dd>{data?.policies.timezone ?? '-'}</dd>
                  </div>
                </dl>
                <div className="thresholds">
                  {defaultRule.warning_before_ms.map((warning) => (
                    <span key={warning}>{formatDuration(warning)}</span>
                  ))}
                </div>
              </div>
            ) : (
              <SkeletonRows rows={4} />
            )}
          </section>

          <section className="panel">
            <div className="panel-header compact">
              <div>
                <h2>Overrides</h2>
                <p>{overrides.length} user rules</p>
              </div>
            </div>
            <div className="override-list">
              {overrides.length > 0 ? (
                overrides.map(([userID, override]) => (
                  <div className="override-row" key={userID}>
                    <div>
                      <strong>{userID}</strong>
                      <span>{override.exempt ? 'Exempt' : override.enabled === false ? 'Disabled' : 'Custom limit'}</span>
                    </div>
                    <span>{override.limit_ms ? formatDuration(override.limit_ms) : '-'}</span>
                  </div>
                ))
              ) : (
                <p className="empty-copy">No per-user overrides configured.</p>
              )}
            </div>
          </section>

          <section className="panel">
            <div className="panel-header compact">
              <div>
                <h2>Runtime</h2>
                <p>Read-only sidecar API</p>
              </div>
            </div>
            <dl className="runtime-list">
              <div>
                <dt>Started</dt>
                <dd>{formatExactDateTime(data?.status.started_at)}</dd>
              </div>
              <div>
                <dt>Last attempt</dt>
                <dd>{formatExactDateTime(data?.status.last_attempt)}</dd>
              </div>
              <div>
                <dt>Config version</dt>
                <dd>{data?.status.config_version ?? '-'}</dd>
              </div>
              <div>
                <dt>Refreshed</dt>
                <dd>{lastRefresh ? formatExactDateTime(lastRefresh.toISOString()) : '-'}</dd>
              </div>
            </dl>
            {(data?.status.last_error || data?.status.config_reload_error) && (
              <div className="runtime-error">
                <WifiOff size={16} />
                <span>{data.status.last_error || data.status.config_reload_error}</span>
              </div>
            )}
          </section>
        </aside>
      </section>
    </main>
  );
}

function resolveServiceState(health: HealthStatus, status: PollStatus) {
  if (health.status === 'healthy' && !status.last_error && !status.config_reload_error) {
    return 'healthy';
  }
  if (health.status === 'degraded' || status.last_error || status.config_reload_error) {
    return 'degraded';
  }
  return health.status || 'unknown';
}

function StatusPill({ state }: { state: string }) {
  const healthy = state === 'healthy';
  const Icon = healthy ? CheckCircle2 : AlertTriangle;
  return (
    <span className={`status-pill ${healthy ? 'ok' : 'warn'}`}>
      <Icon size={16} />
      {titleCase(state)}
    </span>
  );
}

function MetricCard({
  icon,
  label,
  value,
  detail,
  tone = 'neutral',
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  detail: string;
  tone?: 'neutral' | 'ok' | 'warn';
}) {
  return (
    <div className={`metric-card ${tone}`}>
      <div className="metric-icon">{icon}</div>
      <div>
        <span>{label}</span>
        <strong>{value}</strong>
        <p>{detail}</p>
      </div>
    </div>
  );
}

function PlayerTable({ players, loading }: { players: Player[]; loading: boolean }) {
  if (loading) {
    return <SkeletonRows rows={5} />;
  }

  if (players.length === 0) {
    return <div className="empty-state">No online players match the current filter.</div>;
  }

  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Player</th>
            <th>Policy</th>
            <th>Usage</th>
            <th>Reset</th>
            <th>Warning</th>
            <th>Enforcement</th>
          </tr>
        </thead>
        <tbody>
          {players.map((player) => {
            const usage = percent(player.used_ms, player.limit_ms);
            return (
              <tr key={player.user_id}>
                <td>
                  <div className="player-cell">
                    <span className="avatar">{initials(player.name || player.account_name || player.user_id)}</span>
                    <div>
                      <strong>{player.name || player.account_name || player.user_id}</strong>
                      <span>{player.user_id}</span>
                    </div>
                  </div>
                </td>
                <td>
                  <span className={`tag ${player.exempt ? 'neutral' : player.enabled ? 'ok' : 'muted'}`}>
                    {player.exempt ? 'Exempt' : player.enabled ? titleCase(player.period) : 'Disabled'}
                  </span>
                </td>
                <td>
                  <div className="usage-cell">
                    <div className="usage-label">
                      <span>{formatDuration(player.used_ms)}</span>
                      <span>{formatDuration(player.remaining_ms)} left</span>
                    </div>
                    <div className="progress" aria-label={`${usage}% used`}>
                      <span style={{ width: `${usage}%` }} />
                    </div>
                  </div>
                </td>
                <td>
                  <div className="reset-cell">
                    <Clock3 size={15} />
                    <span>{formatDateTime(player.next_reset)}</span>
                  </div>
                </td>
                <td>
                  <div className="warning-list">
                    {player.warnings.length > 0 ? (
                      player.warnings.map((warning) => (
                        <span key={`${player.user_id}-${warning.threshold_ms}-${warning.status}`}>
                          {formatDuration(warning.threshold_ms)} {titleCase(warning.status)}
                        </span>
                      ))
                    ) : (
                      <span className="muted-text">None sent</span>
                    )}
                  </div>
                </td>
                <td>
                  <span className={`tag ${player.enforcement_state ? 'warn' : 'neutral'}`}>
                    {player.enforcement_state ? titleCase(player.enforcement_state) : 'Clear'}
                  </span>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function SkeletonRows({ rows }: { rows: number }) {
  return (
    <div className="skeleton-list">
      {Array.from({ length: rows }, (_, index) => (
        <span className="skeleton-row" key={index} />
      ))}
    </div>
  );
}

function initials(value: string) {
  const cleaned = value.trim();
  if (!cleaned) {
    return 'P';
  }
  const parts = cleaned.split(/\s+/);
  if (parts.length === 1) {
    return parts[0].slice(0, 2).toUpperCase();
  }
  return `${parts[0][0]}${parts[1][0]}`.toUpperCase();
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
