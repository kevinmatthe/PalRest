import { apiBaseUrl } from './config';

export type PollStatus = {
  started_at: string;
  last_attempt?: string;
  last_success?: string;
  last_error?: string;
  online_count: number;
  config_version: number;
  config_reload_error?: string;
};

export type HealthStatus = {
  status: string;
  sqlite?: string;
  last_success?: string;
};

export type Player = {
  user_id: string;
  player_id: string;
  name: string;
  account_name: string;
  online: boolean;
  enabled: boolean;
  exempt: boolean;
  strategy: string;
  period: string;
  used_ms: number;
  remaining_ms: number;
  credit_available_ms?: number;
  last_credit_recovered_ms?: number;
  limit_ms: number;
  period_start: string;
  next_reset: string;
  warning_before_ms: number[];
  enforcement_state?: string;
  warnings: WarningState[];
};

export type WarningState = {
  threshold_ms: number;
  status: string;
  attempts: number;
  next_attempt?: string;
};

export type Rule = {
  enabled: boolean;
  strategy: string;
  period: string;
  reset_at: string;
  reset_weekday?: string;
  limit_ms: number;
  cooldown_every_ms?: number;
  cooldown_rest_ms?: number;
  credit_recover_every_ms?: number;
  credit_recover_amount_ms?: number;
  credit_max_ms?: number;
  warning_before_ms: number[];
};

export type OverrideRule = {
  enabled?: boolean;
  strategy?: string;
  period?: string;
  reset_at?: string;
  reset_weekday?: string;
  limit_ms?: number;
  cooldown_every_ms?: number;
  cooldown_rest_ms?: number;
  credit_recover_every_ms?: number;
  credit_recover_amount_ms?: number;
  credit_max_ms?: number;
  warning_before_ms?: number[];
  exempt: boolean;
};

export type Policies = {
  version: number;
  source: 'database';
  timezone: string;
  default: Rule;
  overrides: Record<string, OverrideRule>;
};

export type PolicyDocument = Omit<Policies, 'version' | 'source'>;

export type PlayersResponse = {
  players: Player[];
};

export type AdminSession = {
  enabled: boolean;
  authenticated: boolean;
  passkey: boolean;
};

export type RankingEntry = {
  user_id: string;
  name: string;
  observed_ms: number;
  online: boolean;
};

export type AnalyticsSummary = {
  online_count: number;
  as_of: string | null;
  today_observed_ms: number;
  peak_count: number;
  peak_at: string | null;
  active_players: number;
  ranking_period: 'today' | 'week';
  ranking: RankingEntry[];
};

export type ConcurrencyPoint = {
  at: string;
  average_count: number | null;
  max_count: number | null;
  coverage: number;
};

export type PlayerActivityDaily = {
  date: string;
  observed_ms: number;
};

export type PlayerActivity = {
  user_id: string;
  name: string;
  daily: PlayerActivityDaily[];
};

export type AnalyticsActivity = {
  range: '7d' | '30d';
  timezone: string;
  start: string;
  end: string;
  concurrency: ConcurrencyPoint[];
  player: PlayerActivity | null;
};

export type TimelineEvent = {
  id: string;
  event_type: string;
  occurred_at: string;
  observed_at: string;
  source: 'palworld_rest' | 'guard' | 'save_snapshot';
  confidence: 'observed' | 'snapshot_derived';
  summary: string;
  data?: Record<string, unknown>;
};

export type TrajectorySample = {
  user_id: string;
  segment_id: string;
  observed_at: string;
  x: number;
  y: number;
  ping: number;
  level: number;
  source_ref: string;
  runtime_epoch: number;
};

export type PlayerPrivateSample = {
  user_id: string;
  observed_at: string;
  ip: string;
  ping: number;
  level: number;
  source_ref: string;
};

export type PlayerTimelineResponse = {
  user_id: string;
  events: TimelineEvent[];
  trajectories: TrajectorySample[];
  private_samples: PlayerPrivateSample[];
  /** Total matching events in range (may exceed events.length when limited). */
  event_total?: number;
  trajectory_total?: number;
  private_sample_total?: number;
};

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function requestJSON<T>(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<T> {
  const response = await fetch(`${apiBaseUrl}${path}`, {
    method: init.method,
    body: init.body,
    credentials: 'same-origin',
    signal,
    headers: {
      Accept: 'application/json',
      ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      ...init.headers,
    },
  });

  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const payload = (await response.json()) as { error?: { message?: string } };
      message = payload.error?.message ?? message;
    } catch {
      // Keep the status text when an endpoint returns a non-JSON error.
    }
    throw new ApiError(message, response.status);
  }

  return (await response.json()) as T;
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  return requestJSON<T>(path, {}, signal);
}

export function getHealth(signal?: AbortSignal) {
  return getJSON<HealthStatus>('/healthz', signal);
}

export function getStatus(signal?: AbortSignal) {
  return getJSON<PollStatus>('/api/v1/status', signal);
}

export function getPlayers(signal?: AbortSignal) {
  return getJSON<PlayersResponse>('/api/v1/players', signal);
}

export function getAnalyticsSummary(ranking: 'today' | 'week', signal?: AbortSignal) {
  const query = new URLSearchParams({ ranking });
  return getJSON<AnalyticsSummary>(`/api/v1/analytics/summary?${query}`, signal);
}

export function getAnalyticsActivity(range: '7d' | '30d', userID?: string, signal?: AbortSignal, includeConcurrency = true) {
  const query = new URLSearchParams({ range });
  if (userID !== undefined) query.set('user_id', userID);
  if (!includeConcurrency) query.set('include_concurrency', 'false');
  return getJSON<AnalyticsActivity>(`/api/v1/analytics/activity?${query}`, signal);
}

export type BehaviorRankSort = 'traveling' | 'stationary' | 'radius' | 'path' | 'active';
export type BehaviorRankRange = 'today' | '7d';

export type BehaviorRankEntry = {
  user_id: string;
  name: string;
  sample_count: number;
  observed_active_ms: number;
  path_length: number;
  radius: number;
  mean_speed: number;
  peak_speed: number;
  traveling_share: number;
  local_share: number;
  stationary_share: number;
  dominant_class: string;
  online: boolean;
};

export type AnalyticsBehaviorRanking = {
  range: BehaviorRankRange;
  sort: BehaviorRankSort;
  start: string;
  end: string;
  timezone: string;
  ranking: BehaviorRankEntry[];
  note?: string;
};

export function getAnalyticsBehavior(
  range: BehaviorRankRange,
  sort: BehaviorRankSort,
  limit = 25,
  signal?: AbortSignal,
) {
  const query = new URLSearchParams({ range, sort, limit: String(limit) });
  return getJSON<AnalyticsBehaviorRanking>(`/api/v1/analytics/behavior?${query}`, signal);
}

export function getPlayerTimeline(userID: string, start: string, end: string, limit = 500, signal?: AbortSignal, includePrivate = false) {
  const query = new URLSearchParams({ start, end, limit: String(limit) });
  const prefix = includePrivate ? '/api/v1/admin/players' : '/api/v1/players';
  return getJSON<PlayerTimelineResponse>(`${prefix}/${encodeURIComponent(userID)}/timeline?${query}`, signal);
}

export type WorldPOI = {
  id: string;
  name_zh: string;
  kind: 'fast_travel' | 'boss_tower' | 'guild_base';
  x: number;
  y: number;
  guild_name?: string;
};

export type PlayerWorldPOIsResponse = {
  user_id: string;
  source: string;
  as_of?: string;
  pois: WorldPOI[];
};

export async function getPlayerWorldPOIs(userID: string, signal?: AbortSignal): Promise<PlayerWorldPOIsResponse> {
  return getJSON<PlayerWorldPOIsResponse>(`/api/v1/players/${encodeURIComponent(userID)}/world-pois`, signal);
}

export function getPolicies(signal?: AbortSignal) {
  return getJSON<Policies>('/api/v1/policies', signal);
}

export function getAdminSession(signal?: AbortSignal) {
  return getJSON<AdminSession>('/api/v1/admin/session', signal);
}

export function loginAdmin(username: string, password: string) {
  return requestJSON<AdminSession>('/api/v1/admin/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });
}

export function logoutAdmin() {
  return requestJSON<AdminSession>('/api/v1/admin/logout', { method: 'POST' });
}

export function resetPlayer(userID: string) {
  return requestJSON<{ status: string; user_id: string }>(`/api/v1/players/${encodeURIComponent(userID)}/reset`, {
    method: 'POST',
  });
}

export function savePolicies(policy: PolicyDocument) {
  return requestJSON<Policies>('/api/v1/policies', {
    method: 'PUT',
    body: JSON.stringify(policy),
  });
}
