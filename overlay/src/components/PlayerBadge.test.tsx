import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import type { Presentation } from '../contracts/presentation'
import { PlayerBadge } from './PlayerBadge'

afterEach(cleanup)

function presentation(displayName = '  Lamball Keeper'): Presentation {
  return {
    schema: 'overlay.presentation/v1',
    game_id: 'palworld',
    user_id: 'steam_uid_must_not_be_displayed',
    observed_at: '2026-07-16T12:00:00Z',
    fresh_until: '2026-07-16T12:00:15Z',
    source_status: 'online',
    identity: { display_name: displayName, level: 42 },
    fields: [],
  }
}

describe('PlayerBadge', () => {
  it('exposes the player initial, level, and source status as one accessible badge', () => {
    render(<PlayerBadge presentation={presentation()} />)

    const badge = screen.getByRole('group', { name: 'Lamball Keeper 玩家徽章' })
    expect(within(badge).getByText('L')).toHaveAttribute('aria-hidden', 'true')
    expect(within(badge).getByText('Lv.42')).toBeInTheDocument()
    expect(within(badge).getByText('在线')).toBeInTheDocument()
    expect(badge).not.toHaveTextContent('steam_uid_must_not_be_displayed')
  })

  it('takes the first visible Unicode character and handles absent levels', () => {
    const value = presentation('  帕鲁朋友')
    delete value.identity.level
    render(<PlayerBadge presentation={value} />)

    const badge = screen.getByRole('group', { name: '帕鲁朋友 玩家徽章' })
    expect(within(badge).getByText('帕')).toBeInTheDocument()
    expect(within(badge).queryByText(/Lv\./)).not.toBeInTheDocument()
  })
})
