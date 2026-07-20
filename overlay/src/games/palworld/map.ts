export type LeafletSimpleCoordinate = [lat: number, lng: number]
export type LeafletSimpleBounds = [
  LeafletSimpleCoordinate,
  LeafletSimpleCoordinate,
]

// Established landscape order: [maxX, maxY, minX, minY].
export const PALWORLD_LANDSCAPE = [349400, 724400, -1099400, -724400] as const

export const PALWORLD_TILE_BOUNDS: LeafletSimpleBounds = [[0, 0], [-256, 256]]

export const PALWORLD_PROJECTION_ID = 'palworld_world_v1'
export const PALWORLD_TILE_SET_ID = 'palworld_default_v1'

const RAW_SPACE_MIN_ABSOLUTE_COORDINATE = 1_000

function isNormalizedRestCoordinate(worldX: number, worldY: number): boolean {
  return worldX >= 0 && worldX <= 256 && worldY >= -256 && worldY <= 0
}

function isPlausibleRawCoordinate(worldX: number, worldY: number): boolean {
  const [maxX, maxY, minX, minY] = PALWORLD_LANDSCAPE
  const insideLandscape = worldX >= minX && worldX <= maxX &&
    worldY >= minY && worldY <= maxY
  const hasRawMagnitude = Math.max(Math.abs(worldX), Math.abs(worldY)) >=
    RAW_SPACE_MIN_ABSOLUTE_COORDINATE
  return insideLandscape && hasRawMagnitude
}

/**
 * Converts Palworld world coordinates to the established CRS.Simple tuple.
 *
 * Leaflet tuple order is [lat/y, lng/x]. REST-normalized coordinates therefore
 * swap their x/y fields. Large raw world coordinates use the established
 * landscape formulas, whose outputs already match the private tile bounds.
 */
export function projectPalworldWorldToLeaflet(
  worldX: number,
  worldY: number,
): LeafletSimpleCoordinate {
  if (!Number.isFinite(worldX) || !Number.isFinite(worldY)) {
    throw new TypeError('Palworld world coordinates must be finite numbers')
  }

  if (isNormalizedRestCoordinate(worldX, worldY)) return [worldY, worldX]
  if (!isPlausibleRawCoordinate(worldX, worldY)) {
    throw new RangeError('Coordinates are outside every known Palworld map space')
  }

  const [maxX, maxY, minX, minY] = PALWORLD_LANDSCAPE
  const latitude = -256 + (256 * (worldX - minX)) / (maxX - minX)
  const longitude = (256 * (worldY - minY)) / (maxY - minY)
  return [latitude, longitude]
}

const INVALID_PERCENT_ESCAPE = /%(?![\da-f]{2})/i

function isHttpProtocol(protocol: string): boolean {
  return protocol === 'http:' || protocol === 'https:'
}

function parseHttpUrl(value: string, base?: URL): URL | null {
  if (value.trim() !== value || value === '' || INVALID_PERCENT_ESCAPE.test(value)) return null

  try {
    const url = base === undefined ? new URL(value) : new URL(value, base)
    if (!isHttpProtocol(url.protocol) || url.username !== '' || url.password !== '') return null
    return url
  } catch {
    return null
  }
}

/** Resolves a tile template without ever crossing the configured private host boundary. */
export function resolvePrivateTileUrl(tileUrl: string, serviceBaseUrl: string): string | null {
  const service = parseHttpUrl(serviceBaseUrl)
  if (service === null) return null

  const resolved = parseHttpUrl(tileUrl, service)
  if (resolved === null || resolved.origin !== service.origin) return null

  return resolved.href.replace(/%7B([zxy])%7D/gi, '{$1}')
}
