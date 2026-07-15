import { StrictMode, useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  AlertTriangle,
  CheckCircle2,
  CircleGauge,
  Clock3,
  Database,
  LogOut,
  RefreshCw,
  Search,
  Shield,
  Users,
  WifiOff,
} from 'lucide-react';
import {
  getHealth,
  getAdminSession,
  getPlayers,
  getPolicies,
  getStatus,
  loginAdmin,
  logoutAdmin,
  resetPlayer,
  savePolicies,
  type AdminSession,
  type HealthStatus,
  type Player,
  type PolicyDocument,
  type Policies,
  type PollStatus,
} from './api';
import { formatDateTime, formatDuration, formatExactDateTime, titleCase } from './utils';
import { AdminLoginModal } from './components/AdminLoginModal';
import { PlayerUsage } from './components/PlayerUsage';
import { PolicyManager } from './components/PolicyManager';
import { AnalyticsDashboard } from './components/AnalyticsDashboard';
import { PlayerTimeline } from './components/PlayerTimeline';
import { LiveMap } from './components/LiveMap';
import { policyCondition } from './policyCondition';
import './styles.css';

type DashboardData = {
  health: HealthStatus;
  status: PollStatus;
  players: Player[];
  policies: Policies;
  admin: AdminSession;
};

type LoadState =
  | { kind: 'loading'; data?: DashboardData; error?: undefined }
  | { kind: 'ready'; data: DashboardData; error?: undefined }
  | { kind: 'error'; data?: DashboardData; error: string };

const refreshIntervalMS = 10_000;

export function App() {
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  const [query, setQuery] = useState('');
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null);
  const [manualRefreshKey, setManualRefreshKey] = useState(0);
  const [adminBusy, setAdminBusy] = useState(false);
  const [view, setView] = useState<'dashboard' | 'analytics' | 'timeline' | 'live' | 'policy'>('dashboard');
  const [analyticsCadenceKey, setAnalyticsCadenceKey] = useState(0);
  const [timelineRefreshKey, setTimelineRefreshKey] = useState(0);
  const [timelineSelectedID, setTimelineSelectedID] = useState('');

  useEffect(() => {
    let mounted = true;
    let controller: AbortController | undefined;

    async function load() {
      controller?.abort();
      controller = new AbortController();
      const requestController = controller;
      setState((current) => ({ kind: 'loading', data: current.data }));
      const adminRequest = getAdminSession(requestController.signal).then((admin) => {
        if (!mounted || requestController.signal.aborted || admin.authenticated) return admin;
        setView((current) => current === 'policy' ? 'dashboard' : current);
        setState((current) => current.data ? { ...current, data: { ...current.data, admin } } : current);
        return admin;
      });
      try {
        const [health, status, playersResponse, policies, admin] = await Promise.all([
          getHealth(requestController.signal),
          getStatus(requestController.signal),
          getPlayers(requestController.signal),
          getPolicies(requestController.signal),
          adminRequest,
        ]);
        if (!mounted || requestController.signal.aborted) {
          return;
        }
        setState({ kind: 'ready', data: { health, status, players: playersResponse.players, policies, admin } });
        if (admin.authenticated) setTimelineRefreshKey((value) => value + 1);
        setLastRefresh(new Date());
      } catch (error) {
        if (!mounted || requestController.signal.aborted) {
          return;
        }
        const message = error instanceof Error ? error.message : '无法加载控制台数据';
        setState((current) => ({ kind: 'error', data: current.data, error: message }));
      }
    }

    void load();
    const timer = window.setInterval(() => {
      setAnalyticsCadenceKey((value) => value + 1);
      void load();
    }, refreshIntervalMS);
    return () => {
      mounted = false;
      controller?.abort();
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
  const defaultCondition = defaultRule ? policyCondition(defaultRule) : undefined;
  const overrides = Object.entries(data?.policies.overrides ?? {});
  const serviceState = data ? resolveServiceState(data.health, data.status) : 'loading';
  const refresh = () => setManualRefreshKey((value) => value + 1);
  const onLogin = async (username: string, password: string) => {
    setAdminBusy(true);
    try {
      await loginAdmin(username, password);
      refresh();
    } finally {
      setAdminBusy(false);
    }
  };
  const onLogout = async () => {
    setAdminBusy(true);
    try {
      await logoutAdmin();
      setView('dashboard');
      refresh();
    } finally {
      setAdminBusy(false);
    }
  };
  const onResetPlayer = async (userID: string) => {
    setAdminBusy(true);
    try {
      await resetPlayer(userID);
      refresh();
    } finally {
      setAdminBusy(false);
    }
  };
  const onSavePolicies = async (next: PolicyDocument) => {
    setAdminBusy(true);
    try {
      await savePolicies(next);
      refresh();
    } finally {
      setAdminBusy(false);
    }
  };

  return (
    <main className="app-shell">
      <header className="topbar">
        <div>
          <p className="eyebrow">PalRest 控制台</p>
          <h1>帕鲁世界游玩时长守护</h1>
        </div>
        <div className="topbar-actions">
          {data?.admin.authenticated && view !== 'policy' && <button className="text-button" type="button" onClick={() => setView('policy')}><Shield size={16} />策略管理</button>}
          <AdminLogin session={data?.admin} busy={adminBusy} onLogin={onLogin} onLogout={onLogout} />
          <StatusPill state={serviceState} />
          <button className="icon-button" type="button" onClick={refresh} title="立即刷新">
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

      {view !== 'policy' ? <nav className="view-tabs" aria-label="控制台视图">
        <button type="button" aria-current={view === 'dashboard' ? 'page' : undefined} onClick={() => setView('dashboard')}>总览</button>
        <button type="button" aria-current={view === 'analytics' ? 'page' : undefined} onClick={() => setView('analytics')}>分析</button>
        <button type="button" aria-current={view === 'live' ? 'page' : undefined} onClick={() => setView('live')}>实时地图</button>
        <button type="button" aria-current={view === 'timeline' ? 'page' : undefined} onClick={() => setView('timeline')}>时间轴</button>
      </nav> : null}

      {view === 'policy' && data?.admin.authenticated ? (
        <PolicyManager policies={data.policies} players={data.players} busy={adminBusy} onSave={onSavePolicies} onBack={() => setView('dashboard')} />
      ) : view === 'analytics' ? <AnalyticsDashboard players={data?.players ?? []} refreshKey={manualRefreshKey + analyticsCadenceKey} />
        : view === 'live' ? (
          <LiveMap
            refreshKey={manualRefreshKey}
            onOpenPlayer={(userID) => {
              setTimelineSelectedID(userID);
              setView('timeline');
            }}
          />
        )
        : view === 'timeline' ? (
          <PlayerTimeline
            includePrivate={data?.admin.authenticated ?? false}
            players={data?.players ?? []}
            refreshKey={manualRefreshKey + timelineRefreshKey}
            initialSelectedID={timelineSelectedID}
          />
        ) : <>
      <section className="status-grid" aria-label="服务状态">
        <MetricCard icon={<Users size={20} />} label="在线玩家" value={activePlayers.toString()} detail={`接口上报 ${data?.status.online_count ?? 0}`} />
        <MetricCard icon={<CircleGauge size={20} />} label="接近限额" value={atRiskPlayers.toString()} detail="剩余不超过 10 分钟" tone={atRiskPlayers > 0 ? 'warn' : 'ok'} />
        <MetricCard icon={<Shield size={20} />} label="已执行限制" value={enforcedPlayers.toString()} detail="当前周期状态" tone={enforcedPlayers > 0 ? 'warn' : 'neutral'} />
        <MetricCard
          icon={<Database size={20} />}
          label="SQLite"
          value={sqliteLabel(data?.health.sqlite)}
          detail={`上次轮询 ${formatDateTime(data?.status.last_success)}`}
          tone={data?.health.sqlite === 'available' ? 'ok' : 'warn'}
        />
      </section>

      <section className="main-grid">
        <section className="panel players-panel">
          <div className="panel-header">
            <div>
              <h2>玩家</h2>
              <p>{state.kind === 'loading' && !data ? '正在加载已知玩家' : `显示 ${players.length} / 共 ${data?.players.length ?? 0} 名`}</p>
            </div>
            <label className="search-box">
              <Search size={16} />
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索玩家" aria-label="搜索玩家" />
            </label>
          </div>
          <PlayerTable players={players} loading={state.kind === 'loading' && !data} admin={data?.admin.authenticated ?? false} busy={adminBusy} onReset={onResetPlayer} />
        </section>

        <aside className="side-stack">
          <section className="panel">
            <div className="panel-header compact">
              <div>
                <h2>策略</h2>
                <p>默认规则</p>
              </div>
            </div>
            {defaultRule ? (
              <div className="policy-summary">
                <div className="policy-state">
                  <span className={`dot ${defaultRule.enabled ? 'ok' : 'muted'}`} />
                  <strong>{defaultRule.enabled ? '已启用' : '已禁用'}</strong>
                </div>
                <dl>
                  <div>
                    <dt>{defaultCondition?.label}</dt>
                    <dd>{formatDuration(defaultCondition?.valueMs)}</dd>
                  </div>
                  <div>
                    <dt>策略</dt>
                    <dd>{strategyLabel(defaultRule.strategy)}</dd>
                  </div>
                  <div>
                    <dt>周期</dt>
                    <dd>{periodLabel(defaultRule.period)}</dd>
                  </div>
                  <div>
                    <dt>重置</dt>
                    <dd>{defaultRule.reset_weekday ? `${defaultRule.reset_weekday} ` : ''}{defaultRule.reset_at}</dd>
                  </div>
                  <div>
                    <dt>时区</dt>
                    <dd>{data?.policies.timezone ?? '-'}</dd>
                  </div>
                </dl>
                <p className="strategy-copy">{strategySummary(defaultRule)}</p>
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
                <h2>覆盖规则</h2>
                <p>{overrides.length} 条用户规则</p>
              </div>
            </div>
            <div className="override-list">
              {overrides.length > 0 ? (
                overrides.map(([userID, override]) => (
                  <div className="override-row" key={userID}>
                    <div>
                      <strong>{userID}</strong>
                      <span>{override.exempt ? '豁免' : override.enabled === false ? '已禁用' : strategyLabel(override.strategy ?? '自定义')}</span>
                    </div>
                    <span>{override.limit_ms ? formatDuration(override.limit_ms) : '-'}</span>
                  </div>
                ))
              ) : (
                <p className="empty-copy">尚未配置按用户覆盖规则。</p>
              )}
            </div>
            {data?.admin.authenticated && <button className="text-button manage-policy-button" type="button" onClick={() => setView('policy')}>管理策略</button>}
          </section>

          <section className="panel">
            <div className="panel-header compact">
              <div>
                <h2>运行时</h2>
                <p>只读旁路 API</p>
              </div>
            </div>
            <dl className="runtime-list">
              <div>
                <dt>启动时间</dt>
                <dd>{formatExactDateTime(data?.status.started_at)}</dd>
              </div>
              <div>
                <dt>上次尝试</dt>
                <dd>{formatExactDateTime(data?.status.last_attempt)}</dd>
              </div>
              <div>
                <dt>配置版本</dt>
                <dd>{data?.status.config_version ?? '-'}</dd>
              </div>
              <div>
                <dt>刷新时间</dt>
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
      </>}
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
      {serviceStateLabel(state)}
    </span>
  );
}

function serviceStateLabel(state: string) {
  switch (state) {
    case 'healthy': return '正常';
    case 'degraded': return '降级';
    case 'loading': return '加载中';
    case 'unknown': return '未知';
    default: return state;
  }
}

function sqliteLabel(value?: string) {
  switch (value) {
    case 'available': return '可用';
    case 'unavailable': return '不可用';
    default: return value ? titleCase(value) : '-';
  }
}

function strategyLabel(value?: string) {
  switch (value) {
    case 'fixed_window': return '固定窗口';
    case 'cooldown': return '冷却';
    case 'credit': return '额度';
    case 'Custom limit':
    case '自定义': return '自定义';
    default: return value ? titleCase(value) : '-';
  }
}

function periodLabel(value?: string) {
  switch (value) {
    case 'daily': return '每日';
    case 'weekly': return '每周';
    default: return value ? titleCase(value) : '-';
  }
}

function warningStatusLabel(status: string) {
  switch (status) {
    case 'sent':
    case 'delivered': return '已送达';
    case 'pending': return '待发送';
    case 'failed': return '失败';
    case 'attempted': return '已尝试';
    default: return titleCase(status);
  }
}

function enforcementLabel(state: string) {
  switch (state) {
    case 'kicked': return '已踢出';
    case 'pending': return '待执行';
    case 'failed': return '失败';
    case 'succeeded': return '成功';
    default: return titleCase(state);
  }
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

function AdminLogin({
  session,
  busy,
  onLogin,
  onLogout,
}: {
  session?: AdminSession;
  busy: boolean;
  onLogin: (username: string, password: string) => Promise<void>;
  onLogout: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);

  if (!session?.enabled) {
    return <span className="status-pill">只读</span>;
  }

  if (session.authenticated) {
    return (
      <button className="text-button" type="button" disabled={busy} onClick={() => void onLogout()} title="退出登录">
        <LogOut size={16} />
        管理员
      </button>
    );
  }

  return <>
    <button className="text-button" type="button" disabled={busy} onClick={() => setOpen(true)}>管理员登录</button>
    <AdminLoginModal open={open} busy={busy} onClose={() => setOpen(false)} onLogin={onLogin} />
  </>;
}

function PlayerTable({
  players,
  loading,
  admin,
  busy,
  onReset,
}: {
  players: Player[];
  loading: boolean;
  admin: boolean;
  busy: boolean;
  onReset: (userID: string) => Promise<void>;
}) {
  if (loading) {
    return <SkeletonRows rows={5} />;
  }

  if (players.length === 0) {
    return <div className="empty-state">没有匹配当前筛选的玩家。</div>;
  }

  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>玩家</th>
            <th>策略</th>
            <th>用量</th>
            <th>重置</th>
            <th>提醒</th>
            <th>执行</th>
            {admin && <th>操作</th>}
          </tr>
        </thead>
        <tbody>
          {players.map((player) => {
            return (
              <tr key={player.user_id}>
                <td>
                  <div className="player-cell">
                    <span className="avatar">{initials(player.name || player.account_name || player.user_id)}</span>
                    <div>
                      <strong>{player.name || player.account_name || player.user_id}</strong>
                      <span>{player.user_id}</span>
                      <span className={`inline-state ${player.online ? 'online' : 'offline'}`}>
                        {player.online ? '在线' : '离线'}
                      </span>
                    </div>
                  </div>
                </td>
                <td>
                  <span className={`tag ${player.exempt ? 'neutral' : player.enabled ? 'ok' : 'muted'}`}>
                    {player.exempt ? '豁免' : player.enabled ? strategyLabel(player.strategy) : '已禁用'}
                  </span>
                </td>
                <td>
                  <PlayerUsage player={player} />
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
                          {formatDuration(warning.threshold_ms)} {warningStatusLabel(warning.status)}
                        </span>
                      ))
                    ) : (
                      <span className="muted-text">未发送</span>
                    )}
                  </div>
                </td>
                <td>
                  <span className={`tag ${player.enforcement_state ? 'warn' : 'neutral'}`}>
                    {player.enforcement_state ? enforcementLabel(player.enforcement_state) : '正常'}
                  </span>
                </td>
                {admin && (
                  <td>
                    <button className="text-button compact-button" type="button" disabled={busy} onClick={() => void onReset(player.user_id)}>
                      重置
                    </button>
                  </td>
                )}
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

function strategySummary(rule: {
  strategy: string;
  cooldown_every_ms?: number;
  cooldown_rest_ms?: number;
  credit_recover_every_ms?: number;
  credit_recover_amount_ms?: number;
  credit_max_ms?: number;
}) {
  if (rule.strategy === 'cooldown') {
    return `游玩 ${formatDuration(rule.cooldown_every_ms)} 后休息 ${formatDuration(rule.cooldown_rest_ms)}。`;
  }
  if (rule.strategy === 'credit') {
    return `每 ${formatDuration(rule.credit_recover_every_ms)} 恢复 ${formatDuration(rule.credit_recover_amount_ms)}，上限 ${formatDuration(rule.credit_max_ms)}。`;
  }
  return '固定重置窗口。';
}

const rootElement = document.getElementById('root');
if (rootElement) {
  createRoot(rootElement).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}
