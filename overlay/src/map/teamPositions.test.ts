import { expect, it } from 'vitest'
import { parseTeamPositions, groupTeamPositions } from './teamPositions'
const snapshot = { as_of: '2026-09-18T00:00:00Z', online_count: 3, positioned: 3, players: [
  { user_id: 'a', name: '<b>A</b>', x: 100, y: -100 },
  { user_id: 'b', name: 'B', x: 100, y: -100 },
  { user_id: 'c', name: 'C', x: 200, y: -150 },
] }
it('validates response and skips unprojectable coordinates without inventing locations', () => {
  const parsed = parseTeamPositions({ ...snapshot, players: [...snapshot.players, { user_id: 'bad', x: NaN, y: 0 }] })
  expect(parsed.players.map(p => p.user_id)).toEqual(['a', 'b', 'c'])
  expect(() => parseTeamPositions({ ...snapshot, as_of: 'bad' })).toThrow()
  expect(() => parseTeamPositions({ ...snapshot, players: null })).toThrow()
})
it('groups overlapping players by screen cell and keeps all members including self', () => {
  const parsed = parseTeamPositions(snapshot)
  const groups = groupTeamPositions(parsed.players, p => ({ x: p.coordinate[1], y: p.coordinate[0] }), 'b')
  expect(groups).toHaveLength(2)
  expect(groups[0]).toMatchObject({ self: true, label: '<b>A</b> · B' })
  expect(groups[0].players.map(p => p.user_id)).toEqual(['a', 'b'])
})
it('keeps marker identity when a whole group moves into another grid cell', () => {
  const players = parseTeamPositions(snapshot).players
  const before = groupTeamPositions(players, p => ({ x: p.coordinate[1], y: p.coordinate[0] }), 'b')
  const after = groupTeamPositions(players, p => ({ x: p.coordinate[1] + 48, y: p.coordinate[0] + 48 }), 'b')
  expect(after.map(g => g.key)).toEqual(before.map(g => g.key))
})
