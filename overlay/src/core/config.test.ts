import { describe, expect, it } from 'vitest'

import { buildOverlayConfig, normalizeBaseUrl, parseOverlayConfig } from './config'

const valid = {
  schema: 1,
  baseUrl: ' https://palbox.tailnet.ts.net:9443/ ',
  gameId: 'palworld',
  userId: ' steam_42 ',
  scale: 1,
  locked: true,
}

describe('overlay config', () => {
  it('normalizes a private service origin without mutating input', () => {
    const input = { ...valid, extra: 'future-safe' }
    expect(parseOverlayConfig(input)).toEqual({
      schema: 1,
      baseUrl: 'https://palbox.tailnet.ts.net:9443',
      gameId: 'palworld',
      userId: 'steam_42',
      scale: 1,
      locked: true,
    })
    expect(input).toEqual({ ...valid, extra: 'future-safe' })
  })

  it('preserves an explicitly configured port, including a scheme default port', () => {
    expect(normalizeBaseUrl('https://PalBox.tailnet.ts.net:443/')).toBe('https://palbox.tailnet.ts.net:443')
    expect(normalizeBaseUrl('http://127.0.0.1:8212')).toBe('http://127.0.0.1:8212')
  })

  it.each([
    'ftp://palbox.test',
    'https://user:secret@palbox.test',
    'https://@palbox.test',
    'https://user:@palbox.test',
    'https://:secret@palbox.test',
    'https://palbox.test/path',
    'https://palbox.test?token=x',
    'https://palbox.test?',
    'https://palbox.test#',
    'https://palbox.test/#part',
    'https:\\palbox.test',
    'https://palbox.test/%zz',
    'https://palbox.test\n',
    'not a url',
  ])('rejects unsafe or ambiguous base URL %s', (value) => {
    expect(() => normalizeBaseUrl(value)).toThrow()
  })

  it.each([
    { ...valid, schema: 2 },
    { ...valid, schema: undefined },
    { ...valid, gameId: 'other' },
    { ...valid, userId: '   ' },
    { ...valid, scale: 0.9 },
    { ...valid, displayId: ' ' },
    { ...valid, x: 10 },
    { ...valid, x: Number.POSITIVE_INFINITY, y: 2 },
    { ...valid, locked: 'yes' },
  ])('rejects an invalid config %#', (input) => {
    expect(parseOverlayConfig(input)).toBeNull()
  })

  it('preserves valid optional geometry as an all-or-nothing pair', () => {
    expect(parseOverlayConfig({ ...valid, displayId: ' screen-1 ', x: -12.5, y: 8 })).toMatchObject({
      displayId: 'screen-1', x: -12.5, y: 8,
    })
  })

  it('builds a normalized schema-one config from a settings draft', () => {
    expect(buildOverlayConfig({ baseUrl: 'http://127.0.0.1:8080/', userId: 'uid', scale: '1.25' }))
      .toEqual({ schema: 1, baseUrl: 'http://127.0.0.1:8080', gameId: 'palworld', userId: 'uid', scale: 1.25, locked: true })
  })
})
