import { describe, expect, it } from 'vitest'

import { buildOverlayConfig, normalizeBaseUrl, parseOverlayConfig } from './config'
import { cloneLayoutProfile, PALWORLD_DEFAULT_LAYOUT, type LayoutProfile } from './layout'

const validV1 = {
  schema: 1,
  baseUrl: ' https://palbox.tailnet.ts.net:9443/ ',
  gameId: 'palworld',
  userId: ' steam_42 ',
  scale: 1,
  locked: true,
}

const customLayout: LayoutProfile = {
  left: { primary: 'player_badge', fallback: 'map' },
  slots: [
    { primary: 'custom.alpha', fallback: 'custom.beta' },
    { primary: 'network.latency', fallback: 'presence.last_online' },
    { primary: 'activity.week', fallback: 'activity.today' },
    { primary: 'policy.remaining', fallback: 'policy.period_end' },
  ],
  progress: { mode: 'field', field: 'custom.progress' },
}

const validV2 = {
  schema: 2,
  baseUrl: 'https://palbox.test',
  gameId: 'custom-game',
  userId: 'uid',
  scale: 1.25,
  locked: false,
  layouts: { 'custom-game': customLayout },
}

describe('overlay config', () => {
  it('migrates schema one to schema two with the exact Palworld default and preserves settings', () => {
    const input = { ...validV1, displayId: ' screen-1 ', x: -12.5, y: 8, extra: 'ignored' }
    expect(parseOverlayConfig(input)).toEqual({
      schema: 2,
      baseUrl: 'https://palbox.tailnet.ts.net:9443',
      gameId: 'palworld',
      userId: 'steam_42',
      scale: 1,
      displayId: 'screen-1',
      x: -12.5,
      y: 8,
      locked: true,
      layouts: { palworld: PALWORLD_DEFAULT_LAYOUT },
    })
    expect(input).toEqual({ ...validV1, displayId: ' screen-1 ', x: -12.5, y: 8, extra: 'ignored' })
  })

  it('round-trips a schema-two custom layout without sharing input references', () => {
    const parsed = parseOverlayConfig(validV2)
    expect(parsed).toEqual(validV2)
    expect(parsed?.layouts['custom-game']).not.toBe(customLayout)
    expect(parsed?.layouts['custom-game'].slots).not.toBe(customLayout.slots)
  })

  it('builds schema two with the edited current-game profile', () => {
    expect(buildOverlayConfig({
      baseUrl: 'http://127.0.0.1:8080/', userId: 'uid', scale: '1.25',
      gameId: 'custom-game', layout: customLayout,
    })).toEqual({
      schema: 2,
      baseUrl: 'http://127.0.0.1:8080',
      gameId: 'custom-game',
      userId: 'uid',
      scale: 1.25,
      locked: true,
      layouts: { 'custom-game': customLayout },
    })
  })

  it('uses the Palworld default when building without an edited profile', () => {
    expect(buildOverlayConfig({ baseUrl: 'https://palbox.test', userId: 'uid', scale: 1 }))
      .toMatchObject({ schema: 2, gameId: 'palworld', layouts: { palworld: PALWORLD_DEFAULT_LAYOUT } })
  })

  it('preserves unknown safe field IDs', () => {
    const input = structuredClone(validV2)
    input.layouts['custom-game'].slots[0] = {
      primary: 'future.field-9_name', fallback: 'another.safe-field',
    }
    expect(parseOverlayConfig(input)?.layouts['custom-game'].slots[0]).toEqual(input.layouts['custom-game'].slots[0])
  })

  it.each([
    ['three slots', (layout: any) => { layout.slots.pop() }],
    ['five slots', (layout: any) => { layout.slots.push({ primary: 'extra.one', fallback: 'extra.two' }) }],
    ['same primary and fallback', (layout: any) => { layout.slots[0].fallback = layout.slots[0].primary }],
    ['unsafe field ID', (layout: any) => { layout.slots[0].primary = 'Bad Field' }],
    ['overlong field ID', (layout: any) => { layout.slots[0].primary = `a${'b'.repeat(96)}` }],
    ['unsafe game ID', (_layout: any, input: any) => { input.gameId = '../palworld' }],
    ['unsafe layout key', (_layout: any, input: any) => { input.layouts['Bad Game'] = input.layouts['custom-game'] }],
    ['same left selection', (layout: any) => { layout.left.fallback = layout.left.primary }],
    ['invalid left selection', (layout: any) => { layout.left.primary = 'portrait' }],
    ['field progress without field', (layout: any) => { layout.progress = { mode: 'field' } }],
    ['hidden progress with field', (layout: any) => { layout.progress = { mode: 'hidden', field: 'custom.progress' } }],
    ['invalid progress mode', (layout: any) => { layout.progress = { mode: 'automatic' } }],
  ])('rejects %s', (_name, mutate) => {
    const input: any = structuredClone(validV2)
    mutate(input.layouts['custom-game'], input)
    expect(parseOverlayConfig(input)).toBeNull()
  })

  it.each([
    { ...validV1, schema: 3 },
    { ...validV1, schema: undefined },
    { ...validV1, gameId: 'other' },
    { ...validV1, userId: '   ' },
    { ...validV1, scale: 0.9 },
    { ...validV1, displayId: ' ' },
    { ...validV1, x: 10 },
    { ...validV1, x: Number.POSITIVE_INFINITY, y: 2 },
    { ...validV1, locked: 'yes' },
  ])('rejects an invalid legacy config %#', (input) => {
    expect(parseOverlayConfig(input)).toBeNull()
  })

  it('preserves an explicitly configured port, including a scheme default port', () => {
    expect(normalizeBaseUrl('https://PalBox.tailnet.ts.net:443/')).toBe('https://palbox.tailnet.ts.net:443')
    expect(normalizeBaseUrl('http://127.0.0.1:8212')).toBe('http://127.0.0.1:8212')
  })

  it.each([
    'ftp://palbox.test', 'https://user:secret@palbox.test', 'https://@palbox.test',
    'https://palbox.test/path', 'https://palbox.test/a/..', 'https://palbox.test/%2e',
    'https://palbox.test//', 'https://palbox.test?token=x', 'https://palbox.test#',
    'https:\\palbox.test', 'https://palbox.test/%zz', 'https://palbox.test\n', 'not a url',
  ])('rejects unsafe or ambiguous base URL %s', (value) => {
    expect(() => normalizeBaseUrl(value)).toThrow()
  })

  it('clones the default used by the test fixture', () => {
    expect(cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)).toEqual(PALWORLD_DEFAULT_LAYOUT)
  })
})
