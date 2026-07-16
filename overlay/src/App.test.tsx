import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { DesktopBridge, FetchSnapshotResult } from './core/bridge'
import type { OverlayConfigV1 } from './core/config'
import App from './App'

afterEach(cleanup)

const config: OverlayConfigV1 = { schema: 1, baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'uid', scale: 1, locked: true }

function bridge(overrides: Partial<DesktopBridge> = {}): DesktopBridge {
  return {
    fetchSnapshot: vi.fn<DesktopBridge['fetchSnapshot']>(async () => ({ status: 503, code: 'snapshot_unavailable' })),
    loadConfig: vi.fn(async () => config), saveConfig: vi.fn(), listPlayers: vi.fn(async () => []),
    currentWindowLabel: vi.fn(async () => 'overlay' as const), setAdjustmentMode: vi.fn(), ...overrides,
  }
}

describe('App window routing', () => {
  it('loads label and config concurrently then renders the settings window', async () => {
    const api = bridge({ currentWindowLabel: vi.fn(async () => 'settings' as const), loadConfig: vi.fn(async () => null) })
    render(<App bridge={api} />)
    expect(api.currentWindowLabel).toHaveBeenCalledTimes(1)
    expect(api.loadConfig).toHaveBeenCalledTimes(1)
    expect(screen.getByText('正在读取本地设置…')).toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: '悬浮条设置' })).toBeInTheDocument()
  })

  it('selects an exact detected Windows UID after the settings player list loads', async () => {
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const),
      loadConfig: vi.fn(async () => null),
      currentPlatform: vi.fn(async () => 'windows'),
      detectedPalworldUserId: vi.fn(async () => 'steam_42'),
      listPlayers: vi.fn(async () => [{ user_id: 'steam_42', name: 'Lamball', account_name: 'keeper' }]),
    })
    render(<App bridge={api} />)
    await screen.findByRole('heading', { name: '悬浮条设置' })
    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'https://palbox.test' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('steam_42'))
  })

  it('does not guess a missing Windows candidate from player names', async () => {
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const), loadConfig: vi.fn(async () => null),
      currentPlatform: vi.fn(async () => 'windows'), detectedPalworldUserId: vi.fn(async () => 'steam_42'),
      listPlayers: vi.fn(async () => [{ user_id: 'other', name: 'steam_42', account_name: 'steam_42' }]),
    })
    render(<App bridge={api} />)
    await screen.findByRole('heading', { name: '悬浮条设置' })
    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'https://palbox.test' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.listPlayers).toHaveBeenCalled())
    expect(screen.getByLabelText('玩家')).toHaveValue('')
  })

  it('keeps macOS manual and never asks for a Windows candidate', async () => {
    const detectedPalworldUserId = vi.fn(async () => 'steam_42')
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const), loadConfig: vi.fn(async () => null),
      currentPlatform: vi.fn(async () => 'macos'), detectedPalworldUserId,
      listPlayers: vi.fn(async () => [{ user_id: 'steam_42', name: 'Lamball', account_name: '' }]),
    })
    render(<App bridge={api} />)
    await screen.findByRole('heading', { name: '悬浮条设置' })
    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'https://palbox.test' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.listPlayers).toHaveBeenCalled())
    expect(detectedPalworldUserId).not.toHaveBeenCalled()
    expect(screen.getByLabelText('玩家')).toHaveValue('')
  })

  it('opens settings once and keeps a compact first-run state in the overlay window', async () => {
    const openSettings = vi.fn(async () => {})
    const api = bridge({ loadConfig: vi.fn(async () => null), openSettings })
    render(<App bridge={api} />)
    expect(await screen.findByText('需要先完成设置')).toBeInTheDocument()
    await waitFor(() => expect(openSettings).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('heading', { name: '悬浮条设置' })).not.toBeInTheDocument()
  })

  it('polls an exact configured player and renders a valid snapshot', async () => {
    const api = bridge({ fetchSnapshot: vi.fn<DesktopBridge['fetchSnapshot']>(async () => ({ status: 200, body: {
      schema: 'overlay.snapshot/v1', game_id: 'palworld', user_id: 'uid', observed_at: '2026-07-16T12:00:00Z',
      fresh_until: '2099-07-16T12:00:00Z', source_status: 'online', capabilities: ['identity'], identity: { display_name: 'Lamball' },
    } })) })
    const { unmount } = render(<App bridge={api} />)
    expect(await screen.findByText('Lamball')).toBeInTheDocument()
    expect(api.fetchSnapshot).toHaveBeenCalledWith({ baseUrl: config.baseUrl, gameId: 'palworld', userId: 'uid' }, expect.any(AbortSignal))
    unmount()
    expect(api.fetchSnapshot).toHaveBeenCalledTimes(1)
  })

  it('stops and aborts an in-flight poll when the overlay unmounts', async () => {
    const api = bridge({ fetchSnapshot: vi.fn<DesktopBridge['fetchSnapshot']>(() => new Promise<FetchSnapshotResult>(() => {})) })
    const { unmount } = render(<App bridge={api} />)
    await waitFor(() => expect(api.fetchSnapshot).toHaveBeenCalledTimes(1))
    const signal = (api.fetchSnapshot as ReturnType<typeof vi.fn>).mock.calls[0][1] as AbortSignal
    unmount()
    expect(signal.aborted).toBe(true)
  })

  it('opens settings for an invalid exact player when the desktop supports it', async () => {
    const openSettings = vi.fn(async () => {})
    const api = bridge({
      openSettings,
      fetchSnapshot: vi.fn<DesktopBridge['fetchSnapshot']>(async () => ({ status: 404, code: 'player_not_found' })),
    })
    render(<App bridge={api} />)
    expect(await screen.findByText('玩家已失效，请在设置中重新选择')).toBeInTheDocument()
    await waitFor(() => expect(openSettings).toHaveBeenCalledTimes(1))
  })

  it('renders safe compact failures without throwing or leaking details', async () => {
    const api = bridge({ loadConfig: vi.fn(async () => { throw new Error('secret path') }) })
    render(<App bridge={api} />)
    expect(await screen.findByText('无法读取悬浮条设置')).toBeInTheDocument()
    expect(screen.queryByText(/secret path/)).not.toBeInTheDocument()
  })
})
