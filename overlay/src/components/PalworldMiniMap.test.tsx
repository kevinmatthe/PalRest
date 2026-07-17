import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { PresentationMap } from '../contracts/presentation'
import { PALWORLD_TILE_BOUNDS } from '../games/palworld/map'
import { PalworldMiniMap } from './PalworldMiniMap'

const leaflet = vi.hoisted(() => {
  const mapInstance = {
    setView: vi.fn(),
    remove: vi.fn(),
  }
  const tileLayerInstance = {
    addTo: vi.fn(),
    on: vi.fn(),
    off: vi.fn(),
    remove: vi.fn(),
  }
  const markerInstance = {
    addTo: vi.fn(),
    setLatLng: vi.fn(),
    remove: vi.fn(),
  }
  return {
    simpleCrs: { code: 'simple' },
    map: vi.fn(() => mapInstance),
    tileLayer: vi.fn(() => tileLayerInstance),
    circleMarker: vi.fn(() => markerInstance),
    mapInstance,
    tileLayerInstance,
    markerInstance,
  }
})

vi.mock('leaflet', () => ({
  CRS: { Simple: leaflet.simpleCrs },
  map: leaflet.map,
  tileLayer: leaflet.tileLayer,
  circleMarker: leaflet.circleMarker,
}))

const SERVICE_BASE = 'https://palbox.tailnet.ts.net:9443/private/'

function position(overrides: Partial<PresentationMap> = {}): PresentationMap {
  return {
    x: 187.25,
    y: -64.5,
    projection: 'palworld_world_v1',
    tile_set: 'palworld_default_v1',
    tile_url: '/map/tiles/{z}/{x}/{y}.png',
    ...overrides,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(cleanup)

describe('PalworldMiniMap', () => {
  it('creates one fixed, north-up, input-free CRS.Simple map with private tiles and marker', () => {
    render(<PalworldMiniMap map={position()} serviceBaseUrl={SERVICE_BASE} />)

    expect(leaflet.map).toHaveBeenCalledTimes(1)
    expect(leaflet.map).toHaveBeenCalledWith(expect.any(HTMLElement), {
      crs: leaflet.simpleCrs,
      attributionControl: false,
      zoomControl: false,
      dragging: false,
      doubleClickZoom: false,
      scrollWheelZoom: false,
      boxZoom: false,
      keyboard: false,
      touchZoom: false,
    })
    expect(leaflet.tileLayer).toHaveBeenCalledTimes(1)
    expect(leaflet.tileLayer).toHaveBeenCalledWith(
      'https://palbox.tailnet.ts.net:9443/map/tiles/{z}/{x}/{y}.png',
      {
        bounds: PALWORLD_TILE_BOUNDS,
        noWrap: true,
        minZoom: 0,
        maxZoom: 0,
        minNativeZoom: 0,
        maxNativeZoom: 0,
      },
    )
    expect(leaflet.tileLayerInstance.addTo).toHaveBeenCalledWith(leaflet.mapInstance)
    expect(leaflet.circleMarker).toHaveBeenCalledWith([-64.5, 187.25], {
      interactive: false,
      radius: 3,
      color: '#eef8f7',
      weight: 1,
      fillColor: '#55e6df',
      fillOpacity: 1,
    })
    expect(leaflet.markerInstance.addTo).toHaveBeenCalledWith(leaflet.mapInstance)
    expect(leaflet.mapInstance.setView).toHaveBeenCalledWith([-64.5, 187.25], 0, { animate: false })
    expect(screen.getByTestId('palworld-mini-map-canvas')).toHaveAttribute('aria-hidden', 'true')
    expect(screen.getByTestId('capability-map')).toHaveStyle({ pointerEvents: 'none' })
  })

  it('recenters without rebuilding only when projected coordinates change', () => {
    const { rerender } = render(
      <PalworldMiniMap map={position()} serviceBaseUrl={SERVICE_BASE} className="first" />,
    )
    vi.clearAllMocks()

    rerender(<PalworldMiniMap map={position()} serviceBaseUrl={SERVICE_BASE} className="second" />)
    expect(leaflet.map).not.toHaveBeenCalled()
    expect(leaflet.mapInstance.setView).not.toHaveBeenCalled()
    expect(leaflet.markerInstance.setLatLng).not.toHaveBeenCalled()

    rerender(<PalworldMiniMap map={position({ x: 42, y: -256 })} serviceBaseUrl={SERVICE_BASE} />)
    expect(leaflet.map).not.toHaveBeenCalled()
    expect(leaflet.tileLayer).not.toHaveBeenCalled()
    expect(leaflet.markerInstance.setLatLng).toHaveBeenCalledWith([-256, 42])
    expect(leaflet.mapInstance.setView).toHaveBeenCalledWith([-256, 42], 0, { animate: false })
  })

  it('rebuilds the map and tile layer when the private tile URL changes', () => {
    const { rerender } = render(<PalworldMiniMap map={position()} serviceBaseUrl={SERVICE_BASE} />)
    vi.clearAllMocks()

    rerender(
      <PalworldMiniMap
        map={position({ tile_url: '/map/v2/{z}/{x}/{y}.webp' })}
        serviceBaseUrl={SERVICE_BASE}
      />,
    )

    expect(leaflet.mapInstance.remove).toHaveBeenCalledTimes(1)
    expect(leaflet.map).toHaveBeenCalledTimes(1)
    expect(leaflet.tileLayer).toHaveBeenCalledWith(
      'https://palbox.tailnet.ts.net:9443/map/v2/{z}/{x}/{y}.webp',
      expect.any(Object),
    )
  })

  it.each([
    position({ projection: 'other_projection' }),
    position({ tile_set: 'other_tile_set' }),
    position({ tile_url: 'https://evil.example/{z}/{x}/{y}.png' }),
  ])('renders a muted unavailable region for unsupported or unsafe map config', (map) => {
    render(<PalworldMiniMap map={map} serviceBaseUrl={SERVICE_BASE} />)

    expect(screen.getByRole('status')).toHaveTextContent('地图不可用')
    expect(leaflet.map).not.toHaveBeenCalled()
  })

  it('contains tile failures locally and keeps an accessible unavailable status', () => {
    render(<PalworldMiniMap map={position()} serviceBaseUrl={SERVICE_BASE} />)
    const tileErrorHandler = leaflet.tileLayerInstance.on.mock.calls.find(
      ([event]) => event === 'tileerror',
    )?.[1] as (() => void) | undefined

    expect(tileErrorHandler).toBeTypeOf('function')
    act(() => tileErrorHandler?.())

    const status = screen.getByRole('status')
    expect(status).toHaveTextContent('地图不可用')
    expect(status).toHaveStyle({
      position: 'absolute',
      inset: '0',
      display: 'flex',
      background: '#040c0f',
    })
    expect(leaflet.mapInstance.remove).not.toHaveBeenCalled()
  })

  it('detaches tile listeners and removes Leaflet resources on unmount', () => {
    const { unmount } = render(<PalworldMiniMap map={position()} serviceBaseUrl={SERVICE_BASE} />)
    const tileErrorHandler = leaflet.tileLayerInstance.on.mock.calls.find(
      ([event]) => event === 'tileerror',
    )?.[1]

    unmount()

    expect(leaflet.tileLayerInstance.off).toHaveBeenCalledWith('tileerror', tileErrorHandler)
    expect(leaflet.markerInstance.remove).toHaveBeenCalledTimes(1)
    expect(leaflet.tileLayerInstance.remove).toHaveBeenCalledTimes(1)
    expect(leaflet.mapInstance.remove).toHaveBeenCalledTimes(1)
  })
})
