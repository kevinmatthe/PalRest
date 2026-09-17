import { MAP_LANDMARKS, type MapLandmark } from '../../../webui/src/map/mapLandmarks'
import {
  PALWORLD_LANDSCAPE,
  projectPalworldWorldToLeaflet,
  type LeafletSimpleCoordinate,
} from '../games/palworld/map'
import type { TeamPositions } from './teamPositions'

export const TELEPORT_DISPLAY_MS = 30_000
export const MAX_TELEPORTS = 20

type TeleportEndpoint = { id: string; name: string; coordinate: LeafletSimpleCoordinate }
export type TeamTeleport = {
  id: string
  userID: string
  playerName: string
  from: TeleportEndpoint
  to: TeleportEndpoint
  observedAt: number
}

export type TeamTeleportOptions = {
  landmarks?: readonly MapLandmark[]
  maxIntervalMs?: number
  /** Radius in raw world units around each fast-travel point. */
  endpointRadius?: number
  /** Minimum actual player displacement, in raw world units. */
  minMovement?: number
}

// The landscape spans the same distance along both axes.
const RAW_UNITS_PER_MAP_UNIT = (PALWORLD_LANDSCAPE[0] - PALWORLD_LANDSCAPE[2]) / 256
// Projection roundoff must not exclude an endpoint exactly on a rule boundary.
const DISTANCE_EPSILON = 1e-6

function projectLandmarks(landmarks: readonly MapLandmark[]): TeleportEndpoint[] {
  const projected: TeleportEndpoint[] = []
  for (const landmark of landmarks) {
    if (landmark.kind !== 'fast_travel') continue
    try {
      projected.push({ id: landmark.id, name: landmark.nameZh, coordinate: projectPalworldWorldToLeaflet(landmark.x, landmark.y) })
    } catch { /* Unknown landmark coordinate spaces cannot establish a teleport. */ }
  }
  return projected
}

const DEFAULT_ENDPOINTS = projectLandmarks(MAP_LANDMARKS)

function validCoordinate(coordinate: LeafletSimpleCoordinate): boolean {
  return Number.isFinite(coordinate[0]) && Number.isFinite(coordinate[1]) &&
    coordinate[0] >= -256 && coordinate[0] <= 0 && coordinate[1] >= 0 && coordinate[1] <= 256
}

function rawDistance(a: LeafletSimpleCoordinate, b: LeafletSimpleCoordinate): number {
  return Math.hypot(a[0] - b[0], a[1] - b[1]) * RAW_UNITS_PER_MAP_UNIT
}

function nearest(coordinate: LeafletSimpleCoordinate, endpoints: TeleportEndpoint[], radius: number): TeleportEndpoint | undefined {
  let best: TeleportEndpoint | undefined
  let distance = Infinity
  for (const endpoint of endpoints) {
    const candidate = rawDistance(coordinate, endpoint.coordinate)
    if (candidate > radius + DISTANCE_EPSILON) continue
    if (candidate < distance || (candidate === distance && (!best || endpoint.id < best.id))) {
      best = endpoint
      distance = candidate
    }
  }
  return best
}

/** Infers possible fast travel; callers must supply consecutive accepted observations. */
export function detectTeamTeleports(
  previous: TeamPositions,
  current: TeamPositions,
  { landmarks, maxIntervalMs = 15_000, endpointRadius = 25_000, minMovement = 50_000 }: TeamTeleportOptions = {},
): TeamTeleport[] {
  const observedAt = Date.parse(current.as_of)
  const elapsed = observedAt - Date.parse(previous.as_of)
  if (!Number.isFinite(elapsed) || elapsed <= 0 || elapsed > maxIntervalMs) return []
  if (![maxIntervalMs, endpointRadius, minMovement].every(value => Number.isFinite(value) && value >= 0)) return []
  const endpoints = landmarks ? projectLandmarks(landmarks) : DEFAULT_ENDPOINTS
  const previousPlayers = new Map(previous.players.map(player => [player.user_id, player]))
  const teleports: TeamTeleport[] = []
  for (const player of current.players) {
    const before = previousPlayers.get(player.user_id)
    if (!before || !validCoordinate(before.coordinate) || !validCoordinate(player.coordinate)) continue
    if (rawDistance(before.coordinate, player.coordinate) + DISTANCE_EPSILON < minMovement) continue
    const from = nearest(before.coordinate, endpoints, endpointRadius)
    const to = nearest(player.coordinate, endpoints, endpointRadius)
    if (!from || !to || from.id === to.id) continue
    teleports.push({
      id: JSON.stringify([player.user_id, previous.as_of, current.as_of, from.id, to.id]),
      userID: player.user_id,
      playerName: player.name,
      from: { ...from, coordinate: [...from.coordinate] },
      to: { ...to, coordinate: [...to.coordinate] },
      observedAt,
    })
  }
  return teleports
}
