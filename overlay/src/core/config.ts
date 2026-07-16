export type OverlayConfigV1 = {
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

export type OverlayConfigDraft = {
  baseUrl: string
  userId: string
  scale: string | number
  locked?: boolean
  displayId?: string
  x?: number
  y?: number
}

const CONTROL_CHARACTER = /[\u0000-\u001f\u007f]/
const MALFORMED_PERCENT = /%(?![0-9a-f]{2})/i
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

export function parseOverlayConfig(input: unknown): OverlayConfigV1 | null {
  if (typeof input !== 'object' || input === null || Array.isArray(input)) return null
  const value = input as Record<string, unknown>
  if (value.schema !== 1 || value.gameId !== 'palworld' || typeof value.userId !== 'string' ||
      !value.userId.trim() || !SCALES.has(value.scale) || typeof value.locked !== 'boolean') return null
  const hasX = value.x !== undefined
  const hasY = value.y !== undefined
  if (hasX !== hasY || (hasX && (!Number.isFinite(value.x) || !Number.isFinite(value.y)))) return null
  if (value.displayId !== undefined &&
      (typeof value.displayId !== 'string' || !value.displayId.trim())) return null
  try {
    const config: OverlayConfigV1 = {
      schema: 1,
      baseUrl: normalizeBaseUrl(value.baseUrl),
      gameId: 'palworld',
      userId: value.userId.trim(),
      scale: value.scale as OverlayConfigV1['scale'],
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

export function buildOverlayConfig(draft: OverlayConfigDraft): OverlayConfigV1 | null {
  const scale = typeof draft.scale === 'string' ? Number(draft.scale) : draft.scale
  return parseOverlayConfig({
    schema: 1,
    baseUrl: draft.baseUrl,
    gameId: 'palworld',
    userId: draft.userId,
    scale,
    locked: draft.locked ?? true,
    displayId: draft.displayId,
    x: draft.x,
    y: draft.y,
  })
}
