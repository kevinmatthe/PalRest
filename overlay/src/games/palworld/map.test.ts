import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

import {
  PALWORLD_LANDSCAPE,
  PALWORLD_TILE_BOUNDS,
  projectPalworldWorldToLeaflet,
  resolvePrivateTileUrl,
} from './map'

describe('Palworld map projection', () => {
  it('documents the established landscape order and tile bounds', () => {
    expect(PALWORLD_LANDSCAPE).toEqual([349400, 724400, -1099400, -724400])
    expect(PALWORLD_TILE_BOUNDS).toEqual([[0, 0], [-256, 256]])
  })

  it('converts normalized REST x/y coordinates to Leaflet [lat/y, lng/x] order', () => {
    expect(projectPalworldWorldToLeaflet(0, 0)).toEqual([0, 0])
    expect(projectPalworldWorldToLeaflet(187.25, -64.5)).toEqual([-64.5, 187.25])
  })

  it.each([
    [0, 0, [0, 0]],
    [256, 0, [0, 256]],
    [0, -256, [-256, 0]],
    [256, -256, [-256, 256]],
  ] as const)('maps normalized edge (%s, %s) inside the real tile bounds', (x, y, expected) => {
    const coordinate = projectPalworldWorldToLeaflet(x, y)
    expect(coordinate).toEqual(expected)
    expect(coordinate[0]).toBeGreaterThanOrEqual(-256)
    expect(coordinate[0]).toBeLessThanOrEqual(0)
    expect(coordinate[1]).toBeGreaterThanOrEqual(0)
    expect(coordinate[1]).toBeLessThanOrEqual(256)
  })

  it('projects known landscape edges exactly like the established webui transform', () => {
    expect(projectPalworldWorldToLeaflet(-1099400, -724400)).toEqual([-256, 0])
    expect(projectPalworldWorldToLeaflet(349400, 724400)).toEqual([0, 256])
    expect(projectPalworldWorldToLeaflet(-1099400, 724400)).toEqual([-256, 256])
    expect(projectPalworldWorldToLeaflet(349400, -724400)).toEqual([0, 0])
  })

  it.each([
    [Number.NaN, 0],
    [0, Number.POSITIVE_INFINITY],
    [Number.NEGATIVE_INFINITY, 0],
  ])('rejects non-finite world coordinates (%s, %s)', (x, y) => {
    expect(() => projectPalworldWorldToLeaflet(x, y)).toThrow('finite')
  })

  it.each([
    [257, -64.5],
    [-1, 2],
    [-1099401, 0],
    [349401, 724401],
  ])('rejects coordinates outside both normalized and plausible raw spaces (%s, %s)', (x, y) => {
    expect(() => projectPalworldWorldToLeaflet(x, y)).toThrow('known Palworld map space')
  })
})

describe('private tile URL boundary', () => {
  const serviceBaseUrl = 'https://palbox.tailnet.ts.net:9443/private/api/'

  it.each([
    ['/map/tiles/{z}/{x}/{y}.png', 'https://palbox.tailnet.ts.net:9443/map/tiles/{z}/{x}/{y}.png'],
    ['tiles/{z}/{x}/{y}.png', 'https://palbox.tailnet.ts.net:9443/private/api/tiles/{z}/{x}/{y}.png'],
    ['https://palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.webp', 'https://palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.webp'],
    ['//palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.png', 'https://palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.png'],
  ])('resolves same-host private URL %s without encoding tile placeholders', (tileUrl, expected) => {
    expect(resolvePrivateTileUrl(tileUrl, serviceBaseUrl)).toBe(expected)
  })

  it.each([
    'https://evil.example/map/{z}/{x}/{y}.png',
    'https://palbox.tailnet.ts.net/map/{z}/{x}/{y}.png',
    'https://palbox.tailnet.ts.net:9444/map/{z}/{x}/{y}.png',
    'https://palbox.tailnet.ts.net.evil.example:9443/map/{z}/{x}/{y}.png',
    'https://evil-palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.png',
    'https://user:password@palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.png',
    'ftp://palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.png',
    'file:///map/{z}/{x}/{y}.png',
    'data:text/plain,tiles',
    'javascript:alert(1)',
    'not a valid url%',
  ])('rejects unsafe tile URL %s', (tileUrl) => {
    expect(resolvePrivateTileUrl(tileUrl, serviceBaseUrl)).toBeNull()
  })

  it.each([
    'file:///private/',
    'https://user:password@palbox.tailnet.ts.net:9443/private/',
    'not a url',
  ])('rejects an unsafe configured service base %s', (baseUrl) => {
    expect(resolvePrivateTileUrl('/map/{z}/{x}/{y}.png', baseUrl)).toBeNull()
  })

  it('rejects an HTTPS-to-HTTP tile downgrade even on the same host and port', () => {
    expect(resolvePrivateTileUrl(
      'http://palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.png',
      serviceBaseUrl,
    )).toBeNull()
  })

  it('requires exact origin for absolute upgrades while relative URLs inherit the configured scheme', () => {
    const httpBase = 'http://palbox.tailnet.ts.net:9443/private/'
    expect(resolvePrivateTileUrl(
      'https://palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.png',
      httpBase,
    )).toBeNull()
    expect(resolvePrivateTileUrl('/map/{z}/{x}/{y}.png', httpBase))
      .toBe('http://palbox.tailnet.ts.net:9443/map/{z}/{x}/{y}.png')
  })

  it('contains no public map fallback in new overlay map sources or resolved URLs', () => {
    const source = [
      readFileSync('src/games/palworld/map.ts', 'utf8'),
      readFileSync('src/components/PalworldMiniMap.tsx', 'utf8'),
      readFileSync('src/components/OverlayBar.tsx', 'utf8'),
    ].join('\n')
    const resolved = resolvePrivateTileUrl('/map/{z}/{x}/{y}.png', serviceBaseUrl)

    expect(source).not.toContain('palworld.gg')
    expect(resolved).not.toContain('palworld.gg')
  })
})
