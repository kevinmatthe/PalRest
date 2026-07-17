// @vitest-environment node

import { describe, expect, expectTypeOf, it } from 'vitest'

import {
  parsePresentation,
  type AvailableDisplayField,
  type DisplayField,
} from './presentation'

function field(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'identity.account',
    label: '账号',
    kind: 'text',
    available: true,
    value: 'Lamball Keeper',
    tone: 'normal',
    ...overrides,
  }
}

function presentation(
  fields: Record<string, unknown>[] = [field()],
): Record<string, unknown> {
  return {
    schema: 'overlay.presentation/v1',
    game_id: 'palworld',
    user_id: 'steam_player',
    observed_at: '2026-07-17T08:00:00Z',
    fresh_until: '2026-07-17T08:00:15.123+00:00',
    source_status: 'online',
    identity: {
      display_name: 'Lamball Keeper',
      account_name: 'keeper',
      level: 42,
    },
    map: {
      x: 123.5,
      y: -456.25,
      projection: 'palworld-world-v1',
      tile_set: 'default',
      tile_url: '/api/v1/map/tiles/{z}/{x}/{y}.png',
    },
    fields,
  }
}

describe('parsePresentation', () => {
  it('parses every available field kind as a discriminated union', () => {
    const parsed = parsePresentation(presentation([
      field({ id: 'identity.account', kind: 'text', value: 'keeper' }),
      field({ id: 'identity.level', kind: 'integer', value: 42 }),
      field({ id: 'activity.today', kind: 'duration_ms', value: 7_200_000 }),
      field({ id: 'presence.last_online', kind: 'timestamp', value: '2026-07-17T15:30:00+08:00' }),
      field({ id: 'network.latency', kind: 'latency_ms', value: 38.5 }),
      field({ id: 'location.coordinates', kind: 'coordinates', value: { x: 123.5, y: -456.25 } }),
      field({ id: 'presence.status', kind: 'status', value: '在线', tone: 'warning' }),
    ]))

    expect(parsed.fields.map(({ kind }) => kind)).toEqual([
      'text',
      'integer',
      'duration_ms',
      'timestamp',
      'latency_ms',
      'coordinates',
      'status',
    ])
    const coordinates = parsed.fields[5]
    if (coordinates.available && coordinates.kind === 'coordinates') {
      expectTypeOf(coordinates.value).toEqualTypeOf<{ x: number; y: number }>()
      expect(coordinates.value).toEqual({ x: 123.5, y: -456.25 })
    } else {
      throw new Error('expected available coordinates')
    }
    expectTypeOf(parsed.fields).toEqualTypeOf<DisplayField[]>()
    expectTypeOf<AvailableDisplayField>().not.toBeAny()
  })

  it('parses unavailable catalog entries without value or progress', () => {
    const rawField = field({
      id: 'network.latency',
      kind: 'latency_ms',
      available: false,
    })
    delete rawField.value
    const parsed = parsePresentation(presentation([
      rawField,
    ]))

    expect(parsed.fields[0]).toEqual({
      id: 'network.latency',
      label: '账号',
      kind: 'latency_ms',
      available: false,
      tone: 'normal',
    })
  })

  it('returns detached nested data without changing the input', () => {
    const input = presentation([
      field({ id: 'location.coordinates', kind: 'coordinates', value: { x: 1, y: 2 } }),
    ])
    const original = structuredClone(input)
    const parsed = parsePresentation(input)

    parsed.identity.display_name = 'Changed'
    parsed.fields[0].label = 'Changed'
    if (parsed.map) parsed.map.x = 0
    if (parsed.fields[0].available && parsed.fields[0].kind === 'coordinates') {
      parsed.fields[0].value.x = 0
    }

    expect(input).toEqual(original)
  })

  it('accepts every finite duration and latency number allowed by the protocol', () => {
    const parsed = parsePresentation(presentation([
      field({ id: 'activity.today', kind: 'duration_ms', value: 1.5 }),
      field({
        id: 'activity.week',
        kind: 'duration_ms',
        value: Number.MAX_SAFE_INTEGER + 1,
      }),
      field({ id: 'network.latency', kind: 'latency_ms', value: -0.5 }),
    ]))

    expect(parsed.fields.map((displayField) => {
      if (!displayField.available) throw new Error('expected available field')
      return displayField.value
    })).toEqual([1.5, Number.MAX_SAFE_INTEGER + 1, -0.5])
  })

  it('accepts a finite mathematical integer outside the safe-integer range', () => {
    const value = Number.MAX_SAFE_INTEGER + 1
    const parsed = parsePresentation(presentation([
      field({ id: 'identity.level', kind: 'integer', value }),
    ]))

    const parsedField = parsed.fields[0]
    if (!parsedField.available || parsedField.kind !== 'integer') {
      throw new Error('expected available integer field')
    }
    expect(parsedField.value).toBe(value)
  })

  it.each([
    ['a duplicate ID', [field(), field()]],
    ['an uppercase ID', [field({ id: 'Network.Latency' })]],
    ['an ID starting with punctuation', [field({ id: '.network' })]],
    ['an ID containing whitespace', [field({ id: 'network latency' })]],
    ['an overlong ID', [field({ id: `a${'b'.repeat(96)}` })]],
  ])('rejects %s', (_name, fields) => {
    expect(() => parsePresentation(presentation(fields))).toThrow()
  })

  it.each([
    ['impossible date', '2026-02-30T12:00:00Z'],
    ['missing zone', '2026-07-17T12:00:00'],
    ['invalid offset', '2026-07-17T12:00:00+24:00'],
    ['non-RFC3339 text', 'tomorrow'],
  ])('rejects %s timestamps', (_name, value) => {
    expect(() => parsePresentation(presentation([
      field({ id: 'presence.last_online', kind: 'timestamp', value }),
    ]))).toThrow(/RFC3339/)
  })

  it.each([
    ['integer fraction', 'integer', 1.5],
    ['NaN integer', 'integer', Number.NaN],
    ['infinite integer', 'integer', Number.POSITIVE_INFINITY],
    ['NaN duration', 'duration_ms', Number.NaN],
    ['infinite latency', 'latency_ms', Number.POSITIVE_INFINITY],
    ['infinite coordinate', 'coordinates', { x: Number.NEGATIVE_INFINITY, y: 0 }],
  ])('rejects %s', (_name, kind, value) => {
    expect(() => parsePresentation(presentation([
      field({ id: 'metric.value', kind, value }),
    ]))).toThrow()
  })

  it.each([
    ['text with number', 'text', 1],
    ['integer with string', 'integer', '1'],
    ['duration with string', 'duration_ms', '1000'],
    ['timestamp with number', 'timestamp', 1],
    ['latency with string', 'latency_ms', '38'],
    ['coordinates with array', 'coordinates', [1, 2]],
    ['coordinates with extra key', 'coordinates', { x: 1, y: 2, z: 3 }],
    ['status with boolean', 'status', true],
  ])('rejects %s', (_name, kind, value) => {
    expect(() => parsePresentation(presentation([
      field({ id: 'metric.value', kind, value }),
    ]))).toThrow()
  })

  it.each([
    ['unsupported kind', { kind: 'percentage' }],
    ['unsupported tone', { tone: 'urgent' }],
    ['progress below zero', { progress: -0.01 }],
    ['progress above one', { progress: 1.01 }],
    ['non-finite progress', { progress: Number.NaN }],
    ['non-number progress', { progress: '0.5' }],
  ])('rejects %s', (_name, overrides) => {
    expect(() => parsePresentation(presentation([field(overrides)]))).toThrow()
  })

  it.each([
    ['value', { available: false, value: 'stale' }],
    ['progress', { available: false, progress: 0.5 }],
  ])('rejects an unavailable field carrying %s', (_name, overrides) => {
    const rawField = field(overrides)
    if ('progress' in overrides) delete rawField.value
    expect(() => parsePresentation(presentation([rawField]))).toThrow()
  })

  it.each([
    ['an extra top-level key', { ...presentation(), future: true }],
    ['a missing top-level key', (() => { const value = presentation(); delete value.fields; return value })()],
    ['an extra field key', presentation([field({ future: true })])],
    ['a missing field key', presentation([(() => { const value = field(); delete value.label; return value })()])],
    ['a non-object field', { ...presentation(), fields: [null] }],
  ])('rejects %s', (_name, value) => {
    expect(() => parsePresentation(value)).toThrow()
  })
})
