export type SourceStatus = 'online' | 'offline' | 'unknown'
export type Capability = 'identity' | 'latency' | 'timers' | 'map'
export type TimerSemantic = 'duration'
export type TimerTone = 'normal' | 'warning' | 'danger' | 'muted'

export interface Snapshot {
  schema: 'overlay.snapshot/v1'
  game_id: string
  user_id: string
  observed_at: string
  fresh_until: string
  source_status: SourceStatus
  capabilities: Capability[]
  identity: Identity
  latency?: Latency
  timers?: Timer[]
  map?: MapPosition
}

export interface Identity {
  display_name: string
  account_name?: string
  level?: number
}

export interface Latency {
  milliseconds: number
}

export interface Timer {
  id: string
  label: string
  value_ms: number
  semantic: TimerSemantic
  tone: TimerTone
  progress?: number
}

export interface MapPosition {
  x: number
  y: number
  projection: string
  tile_set: string
  tile_url: string
}

const SOURCE_STATUSES = new Set<SourceStatus>(['online', 'offline', 'unknown'])
const CAPABILITIES = new Set<Capability>([
  'identity',
  'latency',
  'timers',
  'map',
])
const TIMER_SEMANTICS = new Set<TimerSemantic>(['duration'])
const TIMER_TONES = new Set<TimerTone>(['normal', 'warning', 'danger', 'muted'])
const RFC3339 = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|([+-])(\d{2}):(\d{2}))$/

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function object(value: unknown, path: string): Record<string, unknown> {
  if (!isRecord(value)) throw new Error(`${path} must be an object`)
  return value
}

function string(value: unknown, path: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new Error(`${path} must be a non-empty string`)
  }
  return value
}

function finiteNumber(value: unknown, path: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new Error(`${path} must be a finite number`)
  }
  return value
}

function integer(value: unknown, path: string): number {
  const parsed = finiteNumber(value, path)
  if (!Number.isSafeInteger(parsed)) {
    throw new Error(`${path} must be a safe integer`)
  }
  return parsed
}

function rfc3339(value: unknown, path: string): string {
  const parsed = string(value, path)
  const match = RFC3339.exec(parsed)
  if (!match || Number.isNaN(Date.parse(parsed))) {
    throw new Error(`${path} must be an RFC3339 timestamp`)
  }

  const [, year, month, day, hour, minute, second, , offsetHour, offsetMinute] = match
  const yearNumber = Number(year)
  const monthNumber = Number(month)
  const dayNumber = Number(day)
  const daysInMonth = new Date(Date.UTC(yearNumber, monthNumber, 0)).getUTCDate()
  if (
    monthNumber < 1 || monthNumber > 12 || dayNumber < 1 ||
    dayNumber > daysInMonth || Number(hour) > 23 || Number(minute) > 59 ||
    Number(second) > 59 || Number(offsetHour ?? 0) > 23 ||
    Number(offsetMinute ?? 0) > 59
  ) {
    throw new Error(`${path} must be an RFC3339 timestamp`)
  }
  return parsed
}

function optional<T>(
  value: Record<string, unknown>,
  key: string,
  parse: (field: unknown) => T,
): T | undefined {
  return Object.hasOwn(value, key) ? parse(value[key]) : undefined
}

function parseIdentity(value: unknown): Identity {
  const raw = object(value, 'identity')
  const identity: Identity = { display_name: string(raw.display_name, 'identity.display_name') }
  const accountName = optional(raw, 'account_name', (field) =>
    string(field, 'identity.account_name'))
  const level = optional(raw, 'level', (field) => integer(field, 'identity.level'))
  if (accountName !== undefined) identity.account_name = accountName
  if (level !== undefined) identity.level = level
  return identity
}

function parseLatency(value: unknown): Latency {
  const raw = object(value, 'latency')
  const milliseconds = finiteNumber(raw.milliseconds, 'latency.milliseconds')
  if (milliseconds < 0) throw new Error('latency.milliseconds must not be negative')
  return { milliseconds }
}

function parseTimer(value: unknown, index: number): Timer {
  const path = `timers[${index}]`
  const raw = object(value, path)
  const semantic = string(raw.semantic, `${path}.semantic`)
  const tone = string(raw.tone, `${path}.tone`)
  if (!TIMER_SEMANTICS.has(semantic as TimerSemantic)) {
    throw new Error(`${path}.semantic is unknown`)
  }
  if (!TIMER_TONES.has(tone as TimerTone)) throw new Error(`${path}.tone is unknown`)
  const valueMS = integer(raw.value_ms, `${path}.value_ms`)
  if (valueMS < 0) throw new Error(`${path}.value_ms must not be negative`)

  const timer: Timer = {
    id: string(raw.id, `${path}.id`),
    label: string(raw.label, `${path}.label`),
    value_ms: valueMS,
    semantic: semantic as TimerSemantic,
    tone: tone as TimerTone,
  }
  const progress = optional(raw, 'progress', (field) =>
    finiteNumber(field, `${path}.progress`))
  if (progress !== undefined) {
    if (progress < 0 || progress > 1) {
      throw new Error(`${path}.progress must be between 0 and 1`)
    }
    timer.progress = progress
  }
  return timer
}

function parseTimers(value: unknown): Timer[] {
  if (!Array.isArray(value)) throw new Error('timers must be an array')
  if (value.length === 0) throw new Error('timers must not be empty')
  const timers = value.map(parseTimer)
  const ids = new Set(timers.map((timer) => timer.id))
  if (ids.size !== timers.length) throw new Error('timer IDs must be unique')
  return timers
}

function parseMap(value: unknown): MapPosition {
  const raw = object(value, 'map')
  return {
    x: finiteNumber(raw.x, 'map.x'),
    y: finiteNumber(raw.y, 'map.y'),
    projection: string(raw.projection, 'map.projection'),
    tile_set: string(raw.tile_set, 'map.tile_set'),
    tile_url: string(raw.tile_url, 'map.tile_url'),
  }
}

function parseCapabilities(value: unknown): Capability[] {
  if (!Array.isArray(value)) throw new Error('capabilities must be an array')
  const capabilities = value.map((capability, index) => {
    const parsed = string(capability, `capabilities[${index}]`)
    if (!CAPABILITIES.has(parsed as Capability)) {
      throw new Error(`capabilities[${index}] is unknown`)
    }
    return parsed as Capability
  })
  if (new Set(capabilities).size !== capabilities.length) {
    throw new Error('capabilities must not contain duplicates')
  }
  return capabilities
}

export function parseSnapshot(value: unknown): Snapshot {
  const raw = object(value, 'snapshot')
  if (raw.schema !== 'overlay.snapshot/v1') {
    throw new Error('unsupported snapshot schema')
  }

  const status = string(raw.source_status, 'source_status')
  if (!SOURCE_STATUSES.has(status as SourceStatus)) {
    throw new Error('source_status is unknown')
  }

  const capabilities = parseCapabilities(raw.capabilities)
  const capabilitySet = new Set(capabilities)
  if (!capabilitySet.has('identity')) {
    throw new Error('identity capability is required')
  }

  const latency = optional(raw, 'latency', parseLatency)
  const timers = optional(raw, 'timers', parseTimers)
  const map = optional(raw, 'map', parseMap)
  for (const [capability, section] of [
    ['latency', latency],
    ['timers', timers],
    ['map', map],
  ] as const) {
    if (capabilitySet.has(capability) !== (section !== undefined)) {
      throw new Error(`${capability} capability and section must match`)
    }
  }

  const snapshot: Snapshot = {
    schema: 'overlay.snapshot/v1',
    game_id: string(raw.game_id, 'game_id'),
    user_id: string(raw.user_id, 'user_id'),
    observed_at: rfc3339(raw.observed_at, 'observed_at'),
    fresh_until: rfc3339(raw.fresh_until, 'fresh_until'),
    source_status: status as SourceStatus,
    capabilities,
    identity: parseIdentity(raw.identity),
  }
  if (latency !== undefined) snapshot.latency = latency
  if (timers !== undefined) snapshot.timers = timers
  if (map !== undefined) snapshot.map = map
  return snapshot
}
