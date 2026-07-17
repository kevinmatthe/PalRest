export type SourceStatus = 'online' | 'offline' | 'unknown'
export type DisplayFieldKind =
  | 'text'
  | 'integer'
  | 'duration_ms'
  | 'timestamp'
  | 'latency_ms'
  | 'coordinates'
  | 'status'
export type DisplayTone = 'normal' | 'warning' | 'danger' | 'muted'

export interface PresentationIdentity {
  display_name: string
  account_name?: string
  level?: number
}

export interface PresentationMap {
  x: number
  y: number
  projection: string
  tile_set: string
  tile_url: string
}

interface DisplayFieldBase<K extends DisplayFieldKind> {
  id: string
  label: string
  kind: K
  tone: DisplayTone
}

interface AvailableField<K extends DisplayFieldKind, V> extends DisplayFieldBase<K> {
  available: true
  value: V
  progress?: number
}

export type TextDisplayField = AvailableField<'text', string>
export type IntegerDisplayField = AvailableField<'integer', number>
export type DurationDisplayField = AvailableField<'duration_ms', number>
export type TimestampDisplayField = AvailableField<'timestamp', string>
export type LatencyDisplayField = AvailableField<'latency_ms', number>
export type CoordinatesDisplayField = AvailableField<
  'coordinates',
  { x: number; y: number }
>
export type StatusDisplayField = AvailableField<'status', string>

export type AvailableDisplayField =
  | TextDisplayField
  | IntegerDisplayField
  | DurationDisplayField
  | TimestampDisplayField
  | LatencyDisplayField
  | CoordinatesDisplayField
  | StatusDisplayField

export interface UnavailableDisplayField extends DisplayFieldBase<DisplayFieldKind> {
  available: false
}

export type DisplayField = AvailableDisplayField | UnavailableDisplayField

export interface Presentation {
  schema: 'overlay.presentation/v1'
  game_id: string
  user_id: string
  observed_at: string
  fresh_until: string
  source_status: SourceStatus
  identity: PresentationIdentity
  map?: PresentationMap
  fields: DisplayField[]
}

const RFC3339 = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|([+-])(\d{2}):(\d{2}))$/
const SAFE_FIELD_ID = /^[a-z0-9][a-z0-9._-]{0,95}$/

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function record(value: unknown, path: string): Record<string, unknown> {
  if (!isRecord(value)) {
    throw new Error(`${path} must be an object`)
  }
  return value
}

function exactKeys(
  value: Record<string, unknown>,
  required: readonly string[],
  optional: readonly string[],
  path: string,
): void {
  const allowed = new Set([...required, ...optional])
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) throw new Error(`${path}.${key} is unknown`)
  }
  for (const key of required) {
    if (!Object.hasOwn(value, key)) throw new Error(`${path}.${key} is required`)
  }
}

function nonEmptyString(value: unknown, path: string): string {
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

function safeInteger(value: unknown, path: string): number {
  const parsed = finiteNumber(value, path)
  if (!Number.isSafeInteger(parsed)) throw new Error(`${path} must be a safe integer`)
  return parsed
}

function rfc3339(value: unknown, path: string): string {
  const parsed = nonEmptyString(value, path)
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
    monthNumber < 1 || monthNumber > 12 || dayNumber < 1 || dayNumber > daysInMonth ||
    Number(hour) > 23 || Number(minute) > 59 || Number(second) > 59 ||
    Number(offsetHour ?? 0) > 23 || Number(offsetMinute ?? 0) > 59
  ) {
    throw new Error(`${path} must be an RFC3339 timestamp`)
  }
  return parsed
}

function displayKind(value: unknown, path: string): DisplayFieldKind {
  switch (value) {
    case 'text':
    case 'integer':
    case 'duration_ms':
    case 'timestamp':
    case 'latency_ms':
    case 'coordinates':
    case 'status':
      return value
    default:
      throw new Error(`${path} is unsupported`)
  }
}

function displayTone(value: unknown, path: string): DisplayTone {
  switch (value) {
    case 'normal':
    case 'warning':
    case 'danger':
    case 'muted':
      return value
    default:
      throw new Error(`${path} is unsupported`)
  }
}

function sourceStatus(value: unknown): SourceStatus {
  switch (value) {
    case 'online':
    case 'offline':
    case 'unknown':
      return value
    default:
      throw new Error('source_status is unknown')
  }
}

function progress(value: unknown, path: string): number {
  const parsed = finiteNumber(value, path)
  if (parsed < 0 || parsed > 1) throw new Error(`${path} must be between 0 and 1`)
  return parsed
}

function parseIdentity(value: unknown): PresentationIdentity {
  const raw = record(value, 'identity')
  exactKeys(raw, ['display_name'], ['account_name', 'level'], 'identity')
  const identity: PresentationIdentity = {
    display_name: nonEmptyString(raw.display_name, 'identity.display_name'),
  }
  if (Object.hasOwn(raw, 'account_name')) {
    identity.account_name = nonEmptyString(raw.account_name, 'identity.account_name')
  }
  if (Object.hasOwn(raw, 'level')) identity.level = safeInteger(raw.level, 'identity.level')
  return identity
}

function parseMap(value: unknown): PresentationMap {
  const raw = record(value, 'map')
  exactKeys(raw, ['x', 'y', 'projection', 'tile_set', 'tile_url'], [], 'map')
  return {
    x: finiteNumber(raw.x, 'map.x'),
    y: finiteNumber(raw.y, 'map.y'),
    projection: nonEmptyString(raw.projection, 'map.projection'),
    tile_set: nonEmptyString(raw.tile_set, 'map.tile_set'),
    tile_url: nonEmptyString(raw.tile_url, 'map.tile_url'),
  }
}

function parseCoordinates(value: unknown, path: string): { x: number; y: number } {
  const raw = record(value, path)
  exactKeys(raw, ['x', 'y'], [], path)
  return {
    x: finiteNumber(raw.x, `${path}.x`),
    y: finiteNumber(raw.y, `${path}.y`),
  }
}

function parseField(value: unknown, index: number): DisplayField {
  const path = `fields[${index}]`
  const raw = record(value, path)
  exactKeys(raw, ['id', 'label', 'kind', 'available', 'tone'], ['value', 'progress'], path)

  const id = nonEmptyString(raw.id, `${path}.id`)
  if (!SAFE_FIELD_ID.test(id)) throw new Error(`${path}.id is unsafe`)
  const label = nonEmptyString(raw.label, `${path}.label`)
  const kind = displayKind(raw.kind, `${path}.kind`)
  const tone = displayTone(raw.tone, `${path}.tone`)
  if (typeof raw.available !== 'boolean') throw new Error(`${path}.available must be boolean`)

  if (!raw.available) {
    if (Object.hasOwn(raw, 'value') || Object.hasOwn(raw, 'progress')) {
      throw new Error(`${path} is unavailable but carries value or progress`)
    }
    return { id, label, kind, available: false, tone }
  }
  if (!Object.hasOwn(raw, 'value')) throw new Error(`${path}.value is required`)
  const parsedProgress = Object.hasOwn(raw, 'progress')
    ? progress(raw.progress, `${path}.progress`)
    : undefined
  const common = { id, label, available: true as const, tone }
  const withProgress = parsedProgress === undefined ? {} : { progress: parsedProgress }

  switch (kind) {
    case 'text':
    case 'status': {
      if (typeof raw.value !== 'string') throw new Error(`${path}.value must be a string`)
      return { ...common, kind, value: raw.value, ...withProgress }
    }
    case 'integer':
      return { ...common, kind, value: safeInteger(raw.value, `${path}.value`), ...withProgress }
    case 'duration_ms':
    case 'latency_ms':
      return { ...common, kind, value: finiteNumber(raw.value, `${path}.value`), ...withProgress }
    case 'timestamp':
      return { ...common, kind, value: rfc3339(raw.value, `${path}.value`), ...withProgress }
    case 'coordinates':
      return {
        ...common,
        kind,
        value: parseCoordinates(raw.value, `${path}.value`),
        ...withProgress,
      }
  }
}

function parseFields(value: unknown): DisplayField[] {
  if (!Array.isArray(value)) throw new Error('fields must be an array')
  const fields = value.map(parseField)
  const ids = new Set<string>()
  for (const field of fields) {
    if (ids.has(field.id)) throw new Error(`field ID ${field.id} is duplicate`)
    ids.add(field.id)
  }
  return fields
}

export function parsePresentation(value: unknown): Presentation {
  const raw = record(value, 'presentation')
  exactKeys(
    raw,
    [
      'schema',
      'game_id',
      'user_id',
      'observed_at',
      'fresh_until',
      'source_status',
      'identity',
      'fields',
    ],
    ['map'],
    'presentation',
  )
  if (raw.schema !== 'overlay.presentation/v1') {
    throw new Error('unsupported presentation schema')
  }

  const parsed: Presentation = {
    schema: 'overlay.presentation/v1',
    game_id: nonEmptyString(raw.game_id, 'game_id'),
    user_id: nonEmptyString(raw.user_id, 'user_id'),
    observed_at: rfc3339(raw.observed_at, 'observed_at'),
    fresh_until: rfc3339(raw.fresh_until, 'fresh_until'),
    source_status: sourceStatus(raw.source_status),
    identity: parseIdentity(raw.identity),
    fields: parseFields(raw.fields),
  }
  if (Object.hasOwn(raw, 'map')) parsed.map = parseMap(raw.map)
  return parsed
}
