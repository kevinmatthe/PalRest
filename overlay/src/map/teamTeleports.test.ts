import { describe, expect, it } from 'vitest'
import { MAP_LANDMARKS, type MapLandmark } from '../../../webui/src/map/mapLandmarks'
import { projectPalworldWorldToLeaflet } from '../games/palworld/map'
import { parseTeamPositions, type TeamPositions } from './teamPositions'
import { detectTeamTeleports, MAX_TELEPORTS, TELEPORT_DISPLAY_MS } from './teamTeleports'

const start = Date.parse('2026-09-18T00:00:00Z')
const landmarks: MapLandmark[] = [
  { id: 'a', nameZh: '起点', kind: 'fast_travel', x: -500000, y: 100000 },
  { id: 'b', nameZh: '终点', kind: 'fast_travel', x: -400000, y: 100000 },
]
const options = { landmarks }
function snapshot(time: number, players: Array<{ user_id?: string; name?: string; x: number; y: number }>): TeamPositions {
  return parseTeamPositions({ as_of: new Date(start + time).toISOString(), online_count: players.length, players: players.map(p => ({ user_id: 'u', name: '玩家', ...p })) })
}
const from = snapshot(0, [landmarks[0]])
const to = snapshot(5000, [landmarks[1]])

describe('detectTeamTeleports', () => {
  it('returns deterministic directed POI endpoints, observation time, and current player name', () => {
    const current = snapshot(5000, [{ ...landmarks[1], name: '新名字' }])
    const result = detectTeamTeleports(from, current, options)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({ userID: 'u', playerName: '新名字', observedAt: start + 5000,
      from: { id: 'a', name: '起点', coordinate: projectPalworldWorldToLeaflet(landmarks[0].x, landmarks[0].y) },
      to: { id: 'b', name: '终点', coordinate: projectPalworldWorldToLeaflet(landmarks[1].x, landmarks[1].y) },
    })
    expect(detectTeamTeleports(from, current, options)).toEqual(result)
    expect(TELEPORT_DISPLAY_MS).toBe(30000)
    expect(MAX_TELEPORTS).toBe(20)
  })

  it('uses the shared fast-travel table by default', () => {
    const a = MAP_LANDMARKS.find(p => p.id === 'ft-0')!
    const b = MAP_LANDMARKS.find(p => p.id === 'ft-1')!
    expect(detectTeamTeleports(snapshot(0, [a]), snapshot(5000, [b]))[0]).toMatchObject({ from: { id: a.id }, to: { id: b.id } })
  })

  it('produces equivalent results for raw, normalized, and mixed-coordinate snapshots', () => {
    const normalize = (p: MapLandmark) => {
      const [y, x] = projectPalworldWorldToLeaflet(p.x, p.y)
      return { x, y }
    }
    const normalizedFrom = snapshot(0, [normalize(landmarks[0])])
    const normalizedTo = snapshot(5000, [normalize(landmarks[1])])
    const expected = detectTeamTeleports(from, to, options)
    expect(detectTeamTeleports(normalizedFrom, normalizedTo, options)).toEqual(expected)
    expect(detectTeamTeleports(from, normalizedTo, options)).toEqual(expected)
  })

  it.each([1, 15000])('accepts adjacent observation interval %dms', time => {
    expect(detectTeamTeleports(from, snapshot(time, [landmarks[1]]), options)).toHaveLength(1)
  })
  it.each([-5000, 0, 15001, 60000])('rejects observation interval %dms', time => {
    expect(detectTeamTeleports(from, snapshot(time, [landmarks[1]]), options)).toEqual([])
  })
  it('rejects invalid observation dates', () => {
    expect(detectTeamTeleports({ ...from, as_of: 'unknown' }, to, options)).toEqual([])
    expect(detectTeamTeleports(from, { ...to, as_of: 'unknown' }, options)).toEqual([])
  })

  it('includes exact endpoint-radius and minimum movement boundaries', () => {
    const previous = snapshot(0, [{ x: -475000, y: 100000 }])
    const current = snapshot(5000, [{ x: -425000, y: 100000 }])
    expect(detectTeamTeleports(previous, current, options)).toHaveLength(1)
  })
  it('measures displacement across both projected axes in raw world units', () => {
    const diagonal = { ...landmarks[1], x: landmarks[0].x + 30000, y: landmarks[0].y + 40000 }
    expect(detectTeamTeleports(from, snapshot(5000, [diagonal]), { landmarks: [landmarks[0], diagonal], endpointRadius: 0 })).toHaveLength(1)
    expect(detectTeamTeleports(from, snapshot(5000, [{ ...diagonal, y: diagonal.y - 1 }]), { landmarks: [landmarks[0], diagonal] })).toEqual([])
  })

  it('rejects either endpoint just outside the nearest-POI radius', () => {
    expect(detectTeamTeleports(snapshot(0, [{ x: -525001, y: 100000 }]), to, options)).toEqual([])
    expect(detectTeamTeleports(from, snapshot(5000, [{ x: -374999, y: 100000 }]), options)).toEqual([])
  })
  it('measures actual movement rather than the distance between matched POIs', () => {
    const near: MapLandmark[] = [landmarks[0], { ...landmarks[1], x: -430000 }]
    const previous = snapshot(0, [{ x: -489999, y: 100000 }])
    const current = snapshot(5000, [{ x: -440000, y: 100000 }])
    expect(detectTeamTeleports(previous, current, { landmarks: near })).toEqual([])
  })
  it('does not infer stationary, short, or same-point travel', () => {
    expect(detectTeamTeleports(from, snapshot(5000, [landmarks[0]]), options)).toEqual([])
    expect(detectTeamTeleports(from, snapshot(5000, [{ x: -499000, y: 100000 }]), options)).toEqual([])
    expect(detectTeamTeleports(from, to, { landmarks: [landmarks[0]], endpointRadius: 200000 })).toEqual([])
  })

  it('matches players by ID, ignores arrivals/departures, and detects independent opposite trips', () => {
    const previous = snapshot(0, [{ ...landmarks[0], user_id: 'a' }, { ...landmarks[1], user_id: 'b' }, { ...landmarks[0], user_id: 'gone' }])
    const current = snapshot(5000, [{ ...landmarks[0], user_id: 'b' }, { ...landmarks[1], user_id: 'a' }, { ...landmarks[1], user_id: 'new' }])
    expect(detectTeamTeleports(previous, current, options).map(t => [t.userID, t.from.id, t.to.id]))
      .toEqual([['b', 'b', 'a'], ['a', 'a', 'b']])
    expect(detectTeamTeleports(previous, snapshot(5000, []), options)).toEqual([])
    expect(detectTeamTeleports(snapshot(0, []), current, options)).toEqual([])
  })
  it('skips unknown player/landmark coordinates and excludes towers', () => {
    expect(detectTeamTeleports(snapshot(0, [{ x: NaN, y: 100000 }]), to, options)).toEqual([])
    expect(detectTeamTeleports(from, snapshot(5000, [{ x: 500, y: 500 }]), options)).toEqual([])
    const malformed = { ...to, players: [{ ...to.players[0], coordinate: [NaN, 10] as [number, number] }] }
    expect(detectTeamTeleports(from, malformed, options)).toEqual([])
    expect(detectTeamTeleports(from, to, { landmarks: [landmarks[0], { ...landmarks[1], kind: 'boss_tower' }] })).toEqual([])
    expect(detectTeamTeleports(from, to, { landmarks: [{ ...landmarks[0], x: NaN }, landmarks[1]] })).toEqual([])
  })
  it('uses the nearest point with a deterministic ID tie-break', () => {
    const candidates: MapLandmark[] = [{ ...landmarks[0], id: 'z' }, landmarks[0], landmarks[1], { ...landmarks[1], id: 'far', x: -399000 }]
    expect(detectTeamTeleports(from, to, { landmarks: candidates })[0]).toMatchObject({ from: { id: 'a' }, to: { id: 'b' } })
  })
})
