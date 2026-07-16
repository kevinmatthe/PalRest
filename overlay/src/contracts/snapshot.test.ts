// @vitest-environment node

import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

import { parseSnapshot } from './snapshot'

const fixture = JSON.parse(
  readFileSync(
    new URL('../../../testdata/overlay/palworld_snapshot_v1.json', import.meta.url),
    'utf8',
  ),
) as unknown

function snapshot(): Record<string, unknown> {
  return structuredClone(fixture) as Record<string, unknown>
}

function minimalSnapshot(): Record<string, unknown> {
  const value = snapshot()
  value.capabilities = ['identity']
  delete value.latency
  delete value.timers
  delete value.map
  value.identity = { display_name: 'Player' }
  return value
}

describe('parseSnapshot', () => {
  it('parses the canonical v1 fixture', () => {
    expect(parseSnapshot(fixture).schema).toBe('overlay.snapshot/v1')
  })

  it('rejects a newer schema', () => {
    expect(() =>
      parseSnapshot({ ...snapshot(), schema: 'overlay.snapshot/v2' }),
    ).toThrow(/unsupported snapshot schema/)
  })

  it('accepts additive keys at every object level', () => {
    const value = snapshot()
    value.future = true
    ;(value.identity as Record<string, unknown>).future = true
    ;(value.latency as Record<string, unknown>).future = true
    ;(value.timers as Record<string, unknown>[])[0].future = true
    ;(value.map as Record<string, unknown>).future = true

    expect(parseSnapshot(value)).toMatchObject({
      schema: 'overlay.snapshot/v1',
      identity: { display_name: 'Lamball Keeper' },
    })
    expect(parseSnapshot(value)).not.toHaveProperty('future')
  })

  it.each([
    ['null root', null],
    ['array root', []],
    ['blank game ID', { ...snapshot(), game_id: ' ' }],
    ['missing user ID', (() => { const value = snapshot(); delete value.user_id; return value })()],
    ['invalid observed date', { ...snapshot(), observed_at: '2026-02-30T12:00:00Z' }],
    ['invalid fresh date', { ...snapshot(), fresh_until: 'soon' }],
    ['unknown source status', { ...snapshot(), source_status: 'stale' }],
  ])('rejects %s', (_name, value) => {
    expect(() => parseSnapshot(value)).toThrow()
  })

  it.each([
    ['a non-array', 'identity'],
    ['a non-string item', ['identity', 1]],
    ['duplicates', ['identity', 'identity']],
    ['an unknown capability', ['identity', 'weather']],
  ])('rejects capabilities containing %s', (_name, capabilities) => {
    expect(() => parseSnapshot({ ...snapshot(), capabilities })).toThrow()
  })

  it('allows optional identity fields and optional sections to be omitted', () => {
    const parsed = parseSnapshot(minimalSnapshot())

    expect(parsed.identity).toEqual({ display_name: 'Player' })
    expect(parsed).not.toHaveProperty('latency')
    expect(parsed).not.toHaveProperty('timers')
    expect(parsed).not.toHaveProperty('map')
  })

  it.each([
    ['identity capability', (() => { const value = minimalSnapshot(); value.capabilities = []; return value })()],
    ['identity section', (() => { const value = minimalSnapshot(); delete value.identity; return value })()],
    ['latency capability', (() => { const value = minimalSnapshot(); value.latency = { milliseconds: 1 }; return value })()],
    ['latency section', (() => { const value = minimalSnapshot(); value.capabilities = ['identity', 'latency']; return value })()],
    ['timers capability', (() => { const value = minimalSnapshot(); value.timers = []; return value })()],
    ['timers section', (() => { const value = minimalSnapshot(); value.capabilities = ['identity', 'timers']; return value })()],
    ['map capability', (() => { const value = minimalSnapshot(); value.map = { x: 0, y: 0, projection: 'p', tile_set: 't', tile_url: '/t' }; return value })()],
    ['map section', (() => { const value = minimalSnapshot(); value.capabilities = ['identity', 'map']; return value })()],
  ])('rejects a missing matching %s', (_name, value) => {
    expect(() => parseSnapshot(value)).toThrow()
  })

  it.each([
    ['identity display name', (value: Record<string, unknown>) => { value.identity = { display_name: '' } }],
    ['identity account name', (value: Record<string, unknown>) => { value.identity = { display_name: 'Player', account_name: 1 } }],
    ['identity level', (value: Record<string, unknown>) => { value.identity = { display_name: 'Player', level: 1.5 } }],
    ['latency', (value: Record<string, unknown>) => { value.latency = { milliseconds: Number.NaN } }],
    ['timer value', (value: Record<string, unknown>) => { (value.timers as Record<string, unknown>[])[0].value_ms = 1.5 }],
    ['timer semantic', (value: Record<string, unknown>) => { (value.timers as Record<string, unknown>[])[0].semantic = 'count' }],
    ['timer tone', (value: Record<string, unknown>) => { (value.timers as Record<string, unknown>[])[0].tone = 'urgent' }],
    ['timer progress', (value: Record<string, unknown>) => { (value.timers as Record<string, unknown>[])[0].progress = 1.01 }],
    ['map coordinate', (value: Record<string, unknown>) => { (value.map as Record<string, unknown>).x = Number.POSITIVE_INFINITY }],
    ['map projection', (value: Record<string, unknown>) => { (value.map as Record<string, unknown>).projection = '' }],
  ])('rejects malformed %s', (_name, mutate) => {
    const value = snapshot()
    mutate(value)
    expect(() => parseSnapshot(value)).toThrow()
  })

  it('rejects duplicate timer IDs', () => {
    const value = snapshot()
    const timers = value.timers as Record<string, unknown>[]
    timers[1].id = timers[0].id

    expect(() => parseSnapshot(value)).toThrow()
  })
})
