import type {
  DisplayField,
  DisplayTone,
} from '../contracts/presentation'

export type SlotSelection = { primary: string; fallback: string }
export type LeftSelection = {
  primary: 'map' | 'player_badge'
  fallback: 'map' | 'player_badge'
}
export type ProgressSelection = {
  mode: 'auto' | 'field' | 'hidden'
  field?: string
}
export type LayoutProfile = {
  left: LeftSelection
  slots: [SlotSelection, SlotSelection, SlotSelection, SlotSelection]
  progress: ProgressSelection
}

export const PALWORLD_DEFAULT_LAYOUT: LayoutProfile = {
  left: { primary: 'map', fallback: 'player_badge' },
  slots: [
    { primary: 'network.latency', fallback: 'presence.last_online' },
    { primary: 'activity.today', fallback: 'activity.week' },
    { primary: 'policy.strategy', fallback: 'policy.enforcement' },
    { primary: 'policy.period_end', fallback: 'policy.remaining' },
  ],
  progress: { mode: 'auto', field: 'policy.cycle_used' },
}

export interface ResolvedSlot {
  field: DisplayField
  label: string
  value: string
  tone: DisplayTone
  usedFallback: boolean
}

export interface ResolvedProgress {
  field: DisplayField
  progress: number
  tone: DisplayTone
}

const MINUTE_MS = 60_000
const HOUR_MINUTES = 60
const DAY_MINUTES = 24 * HOUR_MINUTES

function formatDuration(milliseconds: number): string {
  const sign = milliseconds < 0 ? '-' : ''
  const totalMinutes = Math.round(Math.abs(milliseconds) / MINUTE_MS)
  if (totalMinutes === 0) return '0分钟'

  const days = Math.floor(totalMinutes / DAY_MINUTES)
  const hours = Math.floor((totalMinutes % DAY_MINUTES) / HOUR_MINUTES)
  const minutes = totalMinutes % HOUR_MINUTES
  const parts: string[] = []
  if (days > 0) parts.push(`${days}天`)
  if (hours > 0) parts.push(`${hours}小时`)
  if (minutes > 0) parts.push(`${minutes}分钟`)
  return `${sign}${parts.join('')}`
}

function twoDigits(value: number): string {
  return value.toString().padStart(2, '0')
}

function formatTimestamp(value: string, now: Date): string {
  const timestamp = new Date(value)
  const time = `${twoDigits(timestamp.getHours())}:${twoDigits(timestamp.getMinutes())}`
  if (
    timestamp.getFullYear() === now.getFullYear() &&
    timestamp.getMonth() === now.getMonth() &&
    timestamp.getDate() === now.getDate()
  ) {
    return time
  }
  return `${timestamp.getMonth() + 1}月${timestamp.getDate()}日 ${time}`
}

export function formatDisplayField(field: DisplayField, now = new Date()): string {
  if (!field.available) return '--'

  switch (field.kind) {
    case 'text':
    case 'status':
      return field.value
    case 'integer':
      return String(field.value)
    case 'duration_ms':
      return formatDuration(field.value)
    case 'timestamp':
      return formatTimestamp(field.value, now)
    case 'latency_ms':
      return `${Math.round(field.value)} ms`
    case 'coordinates':
      return `${field.value.x}, ${field.value.y}`
  }
}

function placeholder(id: string): DisplayField {
  return {
    id,
    label: id,
    kind: 'text',
    available: false,
    tone: 'muted',
  }
}

export function resolveSlot(
  fields: ReadonlyMap<string, DisplayField>,
  selection: SlotSelection,
): ResolvedSlot {
  const primary = fields.get(selection.primary)
  const fallback = fields.get(selection.fallback)
  if (primary?.available) {
    return {
      field: primary,
      label: primary.label,
      value: formatDisplayField(primary),
      tone: primary.tone,
      usedFallback: false,
    }
  }
  if (fallback?.available) {
    return {
      field: fallback,
      label: fallback.label,
      value: formatDisplayField(fallback),
      tone: fallback.tone,
      usedFallback: true,
    }
  }

  const missing = primary ?? placeholder(selection.primary)
  return {
    field: missing,
    label: missing.label,
    value: '--',
    tone: missing.tone,
    usedFallback: false,
  }
}

export function resolveSlots(
  fields: ReadonlyMap<string, DisplayField>,
  selections: LayoutProfile['slots'],
): [ResolvedSlot, ResolvedSlot, ResolvedSlot, ResolvedSlot] {
  return [
    resolveSlot(fields, selections[0]),
    resolveSlot(fields, selections[1]),
    resolveSlot(fields, selections[2]),
    resolveSlot(fields, selections[3]),
  ]
}

function validProgress(field: DisplayField | undefined): field is DisplayField & {
  available: true
  progress: number
} {
  return field?.available === true &&
    typeof field.progress === 'number' &&
    Number.isFinite(field.progress) &&
    field.progress >= 0 &&
    field.progress <= 1
}

function resolvedProgress(field: DisplayField | undefined): ResolvedProgress | undefined {
  if (!validProgress(field)) return undefined
  return { field, progress: field.progress, tone: field.tone }
}

export function resolveProgress(
  fields: readonly DisplayField[],
  selection: ProgressSelection,
): ResolvedProgress | undefined {
  if (selection.mode === 'hidden') return undefined

  const preferred = selection.field === undefined
    ? undefined
    : fields.find(({ id }) => id === selection.field)
  if (selection.mode === 'field') return resolvedProgress(preferred)

  const selected = resolvedProgress(preferred)
  if (selected !== undefined) return selected
  for (const field of fields) {
    const fallback = resolvedProgress(field)
    if (fallback !== undefined) return fallback
  }
  return undefined
}
