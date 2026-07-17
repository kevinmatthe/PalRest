// @vitest-environment node

import { describe, expect, it } from 'vitest'

import type { DisplayField } from '../contracts/presentation'
import {
  formatDisplayField,
  PALWORLD_DEFAULT_LAYOUT,
  resolveProgress,
  resolveSlot,
  resolveSlots,
  type LayoutProfile,
} from './layout'

function available(
  id: string,
  kind: 'text' | 'status',
  value: string,
  progress?: number,
): DisplayField {
  return {
    id,
    label: `label:${id}`,
    kind,
    available: true,
    value,
    tone: 'normal',
    ...(progress === undefined ? {} : { progress }),
  }
}

function unavailable(id: string, kind: DisplayField['kind'] = 'text'): DisplayField {
  return {
    id,
    label: `label:${id}`,
    kind,
    available: false,
    tone: 'muted',
  }
}

describe('layout field resolution', () => {
  it('uses an available primary field', () => {
    const primary = available('primary', 'text', 'P')
    const fallback = available('fallback', 'text', 'F')
    const resolved = resolveSlot(new Map([[primary.id, primary], [fallback.id, fallback]]), {
      primary: primary.id,
      fallback: fallback.id,
    })

    expect(resolved).toMatchObject({ field: primary, usedFallback: false, value: 'P' })
  })

  it('uses the fallback when primary is unavailable', () => {
    const primary = unavailable('network.latency', 'latency_ms')
    const fallback = available('presence.last_online', 'status', '离线')
    const resolved = resolveSlot(new Map([[primary.id, primary], [fallback.id, fallback]]), {
      primary: primary.id,
      fallback: fallback.id,
    })

    expect(resolved.field.id).toBe('presence.last_online')
    expect(resolved.usedFallback).toBe(true)
  })

  it('preserves the primary label and placeholder when both are unavailable', () => {
    const primary = unavailable('network.latency', 'latency_ms')
    const fallback = unavailable('presence.last_online', 'timestamp')
    const resolved = resolveSlot(new Map([[primary.id, primary], [fallback.id, fallback]]), {
      primary: primary.id,
      fallback: fallback.id,
    })

    expect(resolved).toMatchObject({
      field: primary,
      label: 'label:network.latency',
      value: '--',
      usedFallback: false,
    })
  })

  it('falls back from an unknown primary and gives an unknown double-missing primary a stable label', () => {
    const fallback = available('known.fallback', 'text', 'fallback')
    expect(resolveSlot(new Map([[fallback.id, fallback]]), {
      primary: 'unknown.primary',
      fallback: fallback.id,
    })).toMatchObject({ field: fallback, usedFallback: true })

    expect(resolveSlot(new Map(), {
      primary: 'unknown.primary',
      fallback: 'unknown.fallback',
    })).toMatchObject({
      field: { id: 'unknown.primary', available: false },
      label: 'unknown.primary',
      value: '--',
      usedFallback: false,
    })
  })

  it('always resolves exactly four slots without collapsing missing values', () => {
    const fields = new Map<string, DisplayField>([
      ['one', available('one', 'text', '1')],
      ['four-fallback', available('four-fallback', 'text', '4')],
    ])
    const layout: LayoutProfile = {
      left: { primary: 'map', fallback: 'player_badge' },
      slots: [
        { primary: 'one', fallback: 'one-fallback' },
        { primary: 'two', fallback: 'two-fallback' },
        { primary: 'three', fallback: 'three-fallback' },
        { primary: 'four', fallback: 'four-fallback' },
      ],
      progress: { mode: 'hidden' },
    }

    expect(resolveSlots(fields, layout.slots).map(({ value }) => value)).toEqual([
      '1', '--', '--', '4',
    ])
  })

  it('exports the exact approved Palworld default', () => {
    expect(PALWORLD_DEFAULT_LAYOUT).toEqual({
      left: { primary: 'map', fallback: 'player_badge' },
      slots: [
        { primary: 'network.latency', fallback: 'presence.last_online' },
        { primary: 'activity.today', fallback: 'activity.week' },
        { primary: 'policy.strategy', fallback: 'policy.enforcement' },
        { primary: 'policy.period_end', fallback: 'policy.remaining' },
      ],
      progress: { mode: 'auto', field: 'policy.cycle_used' },
    })
  })

  it('deep-freezes the exported Palworld default template', () => {
    const original = structuredClone(PALWORLD_DEFAULT_LAYOUT)

    expect(Object.isFrozen(PALWORLD_DEFAULT_LAYOUT)).toBe(true)
    expect(Object.isFrozen(PALWORLD_DEFAULT_LAYOUT.left)).toBe(true)
    expect(Object.isFrozen(PALWORLD_DEFAULT_LAYOUT.slots)).toBe(true)
    expect(PALWORLD_DEFAULT_LAYOUT.slots.every(Object.isFrozen)).toBe(true)
    expect(Object.isFrozen(PALWORLD_DEFAULT_LAYOUT.progress)).toBe(true)

    Reflect.set(PALWORLD_DEFAULT_LAYOUT.left, 'primary', 'player_badge')
    Reflect.set(PALWORLD_DEFAULT_LAYOUT.slots[0], 'primary', 'changed.field')
    Reflect.set(PALWORLD_DEFAULT_LAYOUT.progress, 'field', 'changed.progress')
    expect(PALWORLD_DEFAULT_LAYOUT).toEqual(original)
  })
})

describe('resolveProgress', () => {
  const first = available('first', 'text', 'first', 0.1)
  const preferred = available('preferred', 'text', 'preferred', 0.75)
  const withoutProgress = available('without', 'text', 'without')

  it('tries the configured preferred field before Provider order in auto mode', () => {
    expect(resolveProgress([first, preferred], { mode: 'auto', field: 'preferred' }))
      .toMatchObject({ field: preferred, progress: 0.75 })
  })

  it('uses the first available valid progress in Provider order in auto mode', () => {
    expect(resolveProgress([
      unavailable('unavailable'),
      withoutProgress,
      first,
      preferred,
    ], { mode: 'auto', field: 'missing' })).toMatchObject({ field: first, progress: 0.1 })
  })

  it('uses only the named field in field mode', () => {
    expect(resolveProgress([first, preferred], { mode: 'field', field: 'preferred' }))
      .toMatchObject({ field: preferred, progress: 0.75 })
    expect(resolveProgress([first], { mode: 'field', field: 'missing' })).toBeUndefined()
  })

  it('hides hidden or unresolved progress and rejects invalid runtime progress', () => {
    const invalid = { ...first, progress: Number.NaN }
    expect(resolveProgress([first], { mode: 'hidden', field: 'first' })).toBeUndefined()
    expect(resolveProgress([withoutProgress], { mode: 'auto' })).toBeUndefined()
    expect(resolveProgress([invalid], { mode: 'auto' })).toBeUndefined()
  })
})

describe('formatDisplayField', () => {
  it('formats every field kind compactly without changing raw values', () => {
    const duration: DisplayField = {
      id: 'duration', label: 'duration', kind: 'duration_ms', available: true,
      value: 7_200_000, tone: 'normal',
    }
    const timestamp: DisplayField = {
      id: 'timestamp', label: 'timestamp', kind: 'timestamp', available: true,
      value: '2026-01-02T03:04:00Z', tone: 'normal',
    }
    const fields: DisplayField[] = [
      available('text', 'text', 'full untruncated text'),
      { id: 'integer', label: 'integer', kind: 'integer', available: true, value: 42, tone: 'normal' },
      duration,
      timestamp,
      { id: 'latency', label: 'latency', kind: 'latency_ms', available: true, value: 38.5, tone: 'normal' },
      { id: 'coordinates', label: 'coordinates', kind: 'coordinates', available: true, value: { x: 123.5, y: -456.25 }, tone: 'normal' },
      available('status', 'status', '在线'),
    ]

    expect(fields.map((field) => formatDisplayField(field, new Date('2026-01-03T12:00:00Z'))))
      .toEqual([
        'full untruncated text',
        '42',
        '2小时',
        expect.stringMatching(/^1月2日 \d{2}:\d{2}$/),
        '39 ms',
        '123.5, -456.25',
        '在线',
      ])
    expect(duration.value).toBe(7_200_000)
  })

  it('formats a timestamp from the same local day as HH:mm', () => {
    const now = new Date(2026, 6, 17, 20, 0)
    const value = new Date(2026, 6, 17, 8, 5).toISOString()
    const timestamp: DisplayField = {
      id: 'timestamp', label: 'timestamp', kind: 'timestamp', available: true,
      value, tone: 'normal',
    }

    expect(formatDisplayField(timestamp, now)).toBe('08:05')
  })

  it('formats every unavailable field as a placeholder', () => {
    expect(formatDisplayField(unavailable('missing', 'duration_ms'))).toBe('--')
  })
})
