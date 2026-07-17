import {
  cloneLayoutProfile,
  PALWORLD_DEFAULT_LAYOUT,
  type LayoutProfile,
  type ProgressSelection,
} from './layout'

export type LegacyOverlayConfigV1 = {
  schema: 1
  baseUrl: string
  gameId: 'palworld'
  userId: string
  scale: 0.8 | 1 | 1.25
  displayId?: string
  x?: number
  y?: number
  locked: boolean
}

export type OverlayConfigV2 = {
  schema: 2
  baseUrl: string
  gameId: string
  userId: string
  scale: 0.8 | 1 | 1.25
  displayId?: string
  x?: number
  y?: number
  locked: boolean
  layouts: Record<string, LayoutProfile>
}

// Kept as a compatibility surface while schema-one callers are migrated. Parsed and newly
// built configs are always OverlayConfigV2.
export type OverlayConfigV1 = LegacyOverlayConfigV1 | OverlayConfigV2

export type OverlayConfigDraft = {
  baseUrl: string
  userId: string
  scale: string | number
  gameId?: string
  layout?: LayoutProfile
  layouts?: Record<string, LayoutProfile>
  locked?: boolean
  displayId?: string
  x?: number
  y?: number
}

const CONTROL_CHARACTER = /[\u0000-\u001f\u007f]/
const MALFORMED_PERCENT = /%(?![0-9a-f]{2})/i
const SAFE_ID = /^[a-z0-9][a-z0-9._-]{0,95}$/
const SCALES = new Set<unknown>([0.8, 1, 1.25])

export function normalizeBaseUrl(input: unknown): string {
  if (typeof input !== 'string' || CONTROL_CHARACTER.test(input)) {
    throw new Error('invalid base URL')
  }
  const trimmed = input.trim()
  if (!/^https?:\/\//i.test(trimmed) || trimmed.includes('\\') || trimmed.includes('?') ||
      trimmed.includes('#') || MALFORMED_PERCENT.test(trimmed)) {
    throw new Error('invalid base URL')
  }
  const authorityStart = trimmed.indexOf('//') + 2
  const authorityEnd = trimmed.indexOf('/', authorityStart)
  const authority = authorityEnd === -1 ? trimmed.slice(authorityStart) : trimmed.slice(authorityStart, authorityEnd)
  const rawSuffix = authorityEnd === -1 ? '' : trimmed.slice(authorityEnd)
  if (rawSuffix !== '' && rawSuffix !== '/') throw new Error('invalid base URL')
  if (authority.includes('@')) throw new Error('invalid base URL')
  const portDelimiter = authority.startsWith('[')
    ? authority.indexOf(':', authority.indexOf(']') + 1)
    : authority.lastIndexOf(':')
  const explicitPort = portDelimiter === -1 ? '' : authority.slice(portDelimiter + 1)
  if (portDelimiter !== -1 && !/^\d+$/.test(explicitPort)) throw new Error('invalid base URL')
  let url: URL
  try {
    url = new URL(trimmed)
  } catch {
    throw new Error('invalid base URL')
  }
  if ((url.protocol !== 'http:' && url.protocol !== 'https:') ||
      url.username || url.password || url.search || url.hash || url.pathname !== '/') {
    throw new Error('invalid base URL')
  }
  return `${url.protocol}//${url.hostname}${explicitPort ? `:${explicitPort}` : ''}`
}

function record(value: unknown): Record<string, unknown> | null {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null
  try {
    const prototype = Object.getPrototypeOf(value) as unknown
    if (prototype !== Object.prototype && prototype !== null) return null
    const descriptors = Object.getOwnPropertyDescriptors(value)
    if (Object.values(descriptors).some((descriptor) => !Object.hasOwn(descriptor, 'value'))) return null
    return value as Record<string, unknown>
  } catch {
    return null
  }
}

function hasOnlyKeys(value: Record<string, unknown>, required: string[], optional: string[] = []): boolean {
  const allowed = new Set([...required, ...optional])
  return required.every((key) => Object.hasOwn(value, key)) &&
    Object.keys(value).every((key) => allowed.has(key))
}

function safeId(value: unknown): value is string {
  return typeof value === 'string' && SAFE_ID.test(value)
}

function parseLayout(input: unknown): LayoutProfile | null {
  const value = record(input)
  if (!value || !hasOnlyKeys(value, ['left', 'slots', 'progress'])) return null

  const left = record(value.left)
  if (!left || !hasOnlyKeys(left, ['primary', 'fallback']) ||
      !['map', 'player_badge'].includes(left.primary as string) ||
      !['map', 'player_badge'].includes(left.fallback as string) ||
      left.primary === left.fallback) return null

  if (!Array.isArray(value.slots) || value.slots.length !== 4) return null
  const slots = value.slots.map((raw) => {
    const slot = record(raw)
    if (!slot || !hasOnlyKeys(slot, ['primary', 'fallback']) ||
        !safeId(slot.primary) || !safeId(slot.fallback) || slot.primary === slot.fallback) return null
    return { primary: slot.primary, fallback: slot.fallback }
  })
  if (slots.some((slot) => slot === null)) return null

  const progress = record(value.progress)
  if (!progress || !hasOnlyKeys(progress, ['mode'], ['field']) ||
      !['auto', 'field', 'hidden'].includes(progress.mode as string)) return null
  const hasField = Object.hasOwn(progress, 'field')
  if ((hasField && !safeId(progress.field)) ||
      (progress.mode === 'field' && !hasField) ||
      (progress.mode === 'hidden' && hasField)) return null
  const parsedProgress: ProgressSelection = { mode: progress.mode as ProgressSelection['mode'] }
  if (hasField) parsedProgress.field = progress.field as string

  return {
    left: {
      primary: left.primary as LayoutProfile['left']['primary'],
      fallback: left.fallback as LayoutProfile['left']['fallback'],
    },
    slots: slots as LayoutProfile['slots'],
    progress: parsedProgress,
  }
}

function parseLayouts(input: unknown, gameId: string): Record<string, LayoutProfile> | null {
  const value = record(input)
  if (!value || !Object.hasOwn(value, gameId)) return null
  const layouts: Record<string, LayoutProfile> = {}
  for (const [id, rawLayout] of Object.entries(value)) {
    if (!safeId(id)) return null
    const layout = parseLayout(rawLayout)
    if (!layout) return null
    layouts[id] = layout
  }
  return layouts
}

function parseCommon(value: Record<string, unknown>): Omit<OverlayConfigV2, 'schema' | 'gameId' | 'layouts'> | null {
  if (typeof value.userId !== 'string' || !value.userId.trim() ||
      !SCALES.has(value.scale) || typeof value.locked !== 'boolean') return null
  const hasX = value.x !== undefined
  const hasY = value.y !== undefined
  if (hasX !== hasY || (hasX && (!Number.isFinite(value.x) || !Number.isFinite(value.y)))) return null
  if (value.displayId !== undefined &&
      (typeof value.displayId !== 'string' || !value.displayId.trim())) return null
  try {
    const config: Omit<OverlayConfigV2, 'schema' | 'gameId' | 'layouts'> = {
      baseUrl: normalizeBaseUrl(value.baseUrl),
      userId: value.userId.trim(),
      scale: value.scale as OverlayConfigV2['scale'],
      locked: value.locked,
    }
    if (typeof value.displayId === 'string') config.displayId = value.displayId.trim()
    if (hasX) {
      config.x = value.x as number
      config.y = value.y as number
    }
    return config
  } catch {
    return null
  }
}

export function parseOverlayConfig(input: unknown): OverlayConfigV2 | null {
  const value = record(input)
  if (!value || !['schema', 'baseUrl', 'gameId', 'userId', 'scale', 'locked']
    .every((key) => Object.hasOwn(value, key))) return null
  const common = parseCommon(value)
  if (!common) return null
  if (value.schema === 1) {
    if (value.gameId !== 'palworld') return null
    return {
      schema: 2,
      ...common,
      gameId: 'palworld',
      layouts: { palworld: cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT) },
    }
  }
  if (value.schema !== 2 || !safeId(value.gameId) || !Object.hasOwn(value, 'layouts')) return null
  const layouts = parseLayouts(value.layouts, value.gameId)
  if (!layouts) return null
  return { schema: 2, ...common, gameId: value.gameId, layouts }
}

export function buildOverlayConfig(draft: OverlayConfigDraft): OverlayConfigV2 | null {
  const scale = typeof draft.scale === 'string' ? Number(draft.scale) : draft.scale
  const gameId = draft.gameId ?? 'palworld'
  const layout = draft.layout ?? draft.layouts?.[gameId] ??
    (gameId === 'palworld' ? cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT) : null)
  if (!layout) return null
  const layouts = { ...draft.layouts, [gameId]: layout }
  return parseOverlayConfig({
    schema: 2,
    baseUrl: draft.baseUrl,
    gameId,
    userId: draft.userId,
    scale,
    locked: draft.locked ?? true,
    displayId: draft.displayId,
    x: draft.x,
    y: draft.y,
    layouts,
  })
}
