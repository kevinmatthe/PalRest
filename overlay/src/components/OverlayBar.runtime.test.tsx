import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { Presentation } from '../contracts/presentation'
import { cloneLayoutProfile, PALWORLD_DEFAULT_LAYOUT } from '../core/layout'

vi.mock('./PalworldMiniMap', () => ({
  PalworldMiniMap: ({ onUnavailable }: { onUnavailable?: () => void }) => (
    <button data-testid="runtime-map" onClick={onUnavailable}>runtime map</button>
  ),
}))

import { OverlayBar } from './OverlayBar'

afterEach(cleanup)

function presentation(tileUrl = '/tiles/v1/{z}/{x}/{y}.png', userId = 'uid'): Presentation {
  return {
    schema: 'overlay.presentation/v1', game_id: 'palworld', user_id: userId,
    observed_at: '2026-07-16T12:00:00Z', fresh_until: '2099-07-16T12:00:00Z',
    source_status: 'online', identity: { display_name: `Player ${userId}`, level: 12 }, fields: [],
    map: {
      x: 128, y: -128, projection: 'palworld_world_v1', tile_set: 'palworld_default_v1',
      tile_url: tileUrl,
    },
  }
}

describe('OverlayBar runtime map fallback', () => {
  it('falls back to the configured badge and retries only for a new map identity', () => {
    const layout = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    const { rerender } = render(<OverlayBar
      presentation={presentation()}
      layout={layout}
      status="ready"
      adjustMode={false}
      scale={1}
      mapBaseUrl="https://palbox.test"
    />)
    fireEvent.click(screen.getByTestId('runtime-map'))
    expect(screen.getByRole('group', { name: 'Player uid 玩家徽章' })).toBeInTheDocument()

    rerender(<OverlayBar presentation={presentation()} layout={layout} status="ready" adjustMode={false} scale={1} mapBaseUrl="https://palbox.test" />)
    expect(screen.queryByTestId('runtime-map')).not.toBeInTheDocument()

    rerender(<OverlayBar presentation={presentation('/tiles/v2/{z}/{x}/{y}.png')} layout={layout} status="ready" adjustMode={false} scale={1} mapBaseUrl="https://palbox.test" />)
    expect(screen.getByTestId('runtime-map')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('runtime-map'))

    rerender(<OverlayBar presentation={presentation('/tiles/v2/{z}/{x}/{y}.png', 'uid-2')} layout={layout} status="ready" adjustMode={false} scale={1} mapBaseUrl="https://palbox.test" />)
    expect(screen.getByTestId('runtime-map')).toBeInTheDocument()
  })
})
