import { cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Presentation } from '../contracts/presentation'
import { cloneLayoutProfile, PALWORLD_DEFAULT_LAYOUT } from '../core/layout'

const leaflet = vi.hoisted(() => {
  const mapInstance = { setView: vi.fn(), remove: vi.fn() }
  const tileLayerInstance = {
    addTo: vi.fn(), on: vi.fn(), off: vi.fn(), remove: vi.fn(),
  }
  const markerInstance = {
    addTo: vi.fn(), setLatLng: vi.fn(), remove: vi.fn(),
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

import { OverlayBar } from './OverlayBar'

function presentation(x: number, y: number, tileUrl = '/tiles/v1/{z}/{x}/{y}.png'): Presentation {
  return {
    schema: 'overlay.presentation/v1', game_id: 'palworld', user_id: 'uid',
    observed_at: '2026-07-16T12:00:00Z', fresh_until: '2099-07-16T12:00:00Z',
    source_status: 'online', identity: { display_name: 'Player', level: 12 }, fields: [],
    map: {
      x, y, projection: 'palworld_world_v1', tile_set: 'palworld_default_v1',
      tile_url: tileUrl,
    },
  }
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('OverlayBar map resource identity', () => {
  it('updates marker coordinates without remounting Leaflet, but rebuilds for a new tile resource', () => {
    const layout = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    const { rerender } = render(<OverlayBar
      presentation={presentation(128, -128)} layout={layout} status="ready"
      adjustMode={false} scale={1} mapBaseUrl="https://palbox.test"
    />)

    rerender(<OverlayBar
      presentation={presentation(42, -256)} layout={layout} status="ready"
      adjustMode={false} scale={1} mapBaseUrl="https://palbox.test"
    />)

    expect(leaflet.map).toHaveBeenCalledTimes(1)
    expect(leaflet.markerInstance.setLatLng).toHaveBeenCalledWith([-256, 42])
    expect(leaflet.mapInstance.setView).toHaveBeenLastCalledWith([-256, 42], 0, { animate: false })

    rerender(<OverlayBar
      presentation={presentation(42, -256, '/tiles/v2/{z}/{x}/{y}.png')}
      layout={layout} status="ready" adjustMode={false} scale={1}
      mapBaseUrl="https://palbox.test"
    />)
    expect(leaflet.map).toHaveBeenCalledTimes(2)
  })
})
