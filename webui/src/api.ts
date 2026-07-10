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
