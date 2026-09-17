import { projectPalworldWorldToLeaflet, type LeafletSimpleCoordinate } from '../games/palworld/map'

export type TeamPlayer = { user_id: string; name: string; x: number; y: number; coordinate: LeafletSimpleCoordinate }
export type TeamPositions = { as_of: string; online_count: number; players: TeamPlayer[] }
export type TeamGroup = { key: string; coordinate: LeafletSimpleCoordinate; players: TeamPlayer[]; self: boolean; label: string }

export function parseTeamPositions(raw: unknown): TeamPositions {
  if (!raw || typeof raw !== 'object') throw new Error('位置数据格式不兼容')
  const value = raw as Record<string, unknown>
  if (typeof value.as_of !== 'string' || !Number.isFinite(Date.parse(value.as_of)) || !Array.isArray(value.players) ||
    !Number.isSafeInteger(value.online_count) || (value.online_count as number) < 0) throw new Error('位置数据格式不兼容')
  const players: TeamPlayer[] = []
  const seen = new Set<string>()
  for (const entry of value.players) {
    if (!entry || typeof entry !== 'object') continue
    const p = entry as Record<string, unknown>
    if (typeof p.user_id !== 'string' || !p.user_id || seen.has(p.user_id) || typeof p.x !== 'number' || typeof p.y !== 'number') continue
    try {
      const coordinate = projectPalworldWorldToLeaflet(p.x, p.y)
      players.push({ user_id: p.user_id, name: typeof p.name === 'string' && p.name ? p.name : typeof p.account_name === 'string' && p.account_name ? p.account_name : p.user_id, x: p.x, y: p.y, coordinate })
      seen.add(p.user_id)
    } catch { /* Unknown coordinate spaces never become markers at the origin. */ }
  }
  return { as_of: value.as_of, online_count: value.online_count as number, players }
}

export function groupTeamPositions(players: TeamPlayer[], project: (player: TeamPlayer) => { x: number; y: number }, selfID: string): TeamGroup[] {
  const groups = new Map<string, TeamGroup>()
  for (const player of players) {
    const pixel = project(player)
    const key = `${Math.floor(pixel.x / 24)}:${Math.floor(pixel.y / 24)}`
    let group = groups.get(key)
    if (!group) {
      group = { key, coordinate: player.coordinate, players: [], self: false, label: '' }
      groups.set(key, group)
    }
    group.players.push(player)
    group.self ||= player.user_id === selfID
  }
  for (const group of groups.values()) {
    group.key = JSON.stringify(group.players.map(p => p.user_id).sort())
    group.label = group.players.slice(0, 2).map(p => p.name).join(' · ') + (group.players.length > 2 ? ` +${group.players.length - 2}` : '')
  }
  return [...groups.values()]
}
