import { readFileSync } from 'node:fs'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { DesktopBridge, FetchSnapshotResult } from './core/bridge'
import type { OverlayConfigV1 } from './core/config'
import App from './App'

afterEach(cleanup)

const config: OverlayConfigV1 = { schema: 1, baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'uid', scale: 1, locked: true }

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise })
  return { promise, resolve }
}

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

  it('does not start detected-user work after unmount while platform is pending', async () => {
    let resolvePlatform!: (platform: string) => void
    const currentPlatform = vi.fn(() => new Promise<string>((resolve) => { resolvePlatform = resolve }))
    const detectedPalworldUserId = vi.fn(async () => 'steam_42')
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const), loadConfig: vi.fn(async () => null),
      currentPlatform, detectedPalworldUserId,
    })
    const { unmount } = render(<App bridge={api} />)
    await waitFor(() => expect(currentPlatform).toHaveBeenCalledTimes(1))
    unmount()
    resolvePlatform('windows')
    await Promise.resolve()
    await Promise.resolve()
    expect(detectedPalworldUserId).not.toHaveBeenCalled()
  })

  it('mounts through the exported desktop bridge factory', () => {
    const source = readFileSync('src/main.tsx', 'utf8')
    expect(source).toMatch(/import\s+\{\s*createDesktopBridge\s*\}\s+from\s+['"]\.\/core\/bridge['"]/)
    expect(source).toMatch(/<App\s+bridge=\{createDesktopBridge\(\)\}\s*\/>/)
    expect(source).not.toContain('createBrowserPlaceholderBridge')
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

  it('reflects native adjustment events and unregisters the listener on unmount', async () => {
    let notify!: (enabled: boolean) => void
    const unlisten = vi.fn()
    const api = bridge({
      onAdjustmentModeChanged: vi.fn(async (handler) => { notify = handler; return unlisten }),
      fetchSnapshot: vi.fn<DesktopBridge['fetchSnapshot']>(async () => ({ status: 200, body: {
        schema: 'overlay.snapshot/v1', game_id: 'palworld', user_id: 'uid', observed_at: '2026-07-16T12:00:00Z',
        fresh_until: '2099-07-16T12:00:00Z', source_status: 'online', capabilities: ['identity'], identity: { display_name: 'Lamball' },
      } })),
    })
    const { unmount } = render(<App bridge={api} />)
    await screen.findByText('Lamball')
    notify(true)
    expect(await screen.findByText('拖动调整位置')).toBeInTheDocument()
    notify(false)
    await waitFor(() => expect(screen.queryByText('拖动调整位置')).not.toBeInTheDocument())
    unmount()
    expect(unlisten).toHaveBeenCalledTimes(1)
  })

  it('unregisters a lifecycle listener that resolves after unmount', async () => {
    let resolveListener!: (unlisten: () => void) => void
    const unlisten = vi.fn()
    const api = bridge({
      onAdjustmentModeChanged: vi.fn(() => new Promise<() => void>((resolve) => { resolveListener = resolve })),
    })
    const { unmount } = render(<App bridge={api} />)
    unmount()
    resolveListener(unlisten)
    await waitFor(() => expect(unlisten).toHaveBeenCalledTimes(1))
  })

  it('routes reselect events into settings and does not restore the saved UID', async () => {
    let reselect!: () => void
    const listPlayers = vi.fn(async () => [{ user_id: 'uid', name: 'Player', account_name: '' }])
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const),
      onReselectPlayer: vi.fn(async (handler) => { reselect = handler; return () => {} }),
      listPlayers,
    })
    render(<App bridge={api} />)
    await screen.findByRole('heading', { name: '悬浮条设置' })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid'))
    reselect()
    await waitFor(() => expect(listPlayers).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue(''))
  })

  it('stops and aborts an in-flight poll when the overlay unmounts', async () => {
    const api = bridge({ fetchSnapshot: vi.fn<DesktopBridge['fetchSnapshot']>(() => new Promise<FetchSnapshotResult>(() => {})) })
    const { unmount } = render(<App bridge={api} />)
    await waitFor(() => expect(api.fetchSnapshot).toHaveBeenCalledTimes(1))
    const signal = (api.fetchSnapshot as ReturnType<typeof vi.fn>).mock.calls[0][1] as AbortSignal
    unmount()
    expect(signal.aborted).toBe(true)
  })

  it('aborts the old poll and immediately requests a newly saved overlay config', async () => {
    let publishConfig!: (value: unknown) => void
    const oldRequest = deferred<FetchSnapshotResult>()
    const fetchSnapshot = vi.fn<DesktopBridge['fetchSnapshot']>((request) => {
      if (request.userId === 'uid') return oldRequest.promise
      return Promise.resolve({ status: 503, code: 'snapshot_unavailable' })
    })
    const api = bridge({
      fetchSnapshot,
      onConfigChanged: vi.fn(async (handler) => { publishConfig = handler; return () => {} }),
    })
    render(<App bridge={api} />)
    await waitFor(() => expect(fetchSnapshot).toHaveBeenCalledWith(
      { baseUrl: config.baseUrl, gameId: config.gameId, userId: 'uid' }, expect.any(AbortSignal),
    ))
    const oldSignal = fetchSnapshot.mock.calls[0][1]

    act(() => publishConfig({ ...config, baseUrl: 'https://replacement.test', userId: 'uid-2' }))

    await waitFor(() => expect(fetchSnapshot).toHaveBeenCalledWith(
      { baseUrl: 'https://replacement.test', gameId: config.gameId, userId: 'uid-2' }, expect.any(AbortSignal),
    ))
    expect(oldSignal.aborted).toBe(true)
  })

  it('ignores invalid native config payloads', async () => {
    let publishConfig!: (value: unknown) => void
    const fetchSnapshot = vi.fn<DesktopBridge['fetchSnapshot']>(() => new Promise(() => {}))
    const api = bridge({
      fetchSnapshot,
      onConfigChanged: vi.fn(async (handler) => { publishConfig = handler; return () => {} }),
    })
    render(<App bridge={api} />)
    await waitFor(() => expect(fetchSnapshot).toHaveBeenCalledTimes(1))
    const oldSignal = fetchSnapshot.mock.calls[0][1]

    act(() => publishConfig({ ...config, userId: '' }))

    await Promise.resolve()
    expect(fetchSnapshot).toHaveBeenCalledTimes(1)
    expect(oldSignal.aborted).toBe(false)
  })

  it('unregisters the config listener on unmount', async () => {
    const unlisten = vi.fn()
    const api = bridge({
      onConfigChanged: vi.fn(async () => unlisten),
      fetchSnapshot: vi.fn<DesktopBridge['fetchSnapshot']>(() => new Promise(() => {})),
    })
    const { unmount } = render(<App bridge={api} />)
    await waitFor(() => expect(api.onConfigChanged).toHaveBeenCalledTimes(1))
    unmount()
    expect(unlisten).toHaveBeenCalledTimes(1)
  })

  it('does not apply overlay config events to the settings window bootstrap', async () => {
    let publishConfig!: (value: unknown) => void
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const),
      loadConfig: vi.fn(async () => null),
      onConfigChanged: vi.fn(async (handler) => { publishConfig = handler; return () => {} }),
    })
    render(<App bridge={api} />)
    await screen.findByRole('heading', { name: '悬浮条设置' })
    await waitFor(() => expect(api.onConfigChanged).toHaveBeenCalledTimes(1))

    act(() => publishConfig({ ...config, baseUrl: 'https://replacement.test', userId: 'uid-2' }))

    expect(screen.queryByRole('button', { name: '调整悬浮条位置' })).not.toBeInTheDocument()
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

  it('keeps compact loading and disconnected states draggable during adjustment', async () => {
    let notify!: (enabled: boolean) => void
    const api = bridge({
      onAdjustmentModeChanged: vi.fn(async (handler) => { notify = handler; return () => {} }),
      fetchSnapshot: vi.fn<DesktopBridge['fetchSnapshot']>(() => new Promise(() => {})),
    })
    render(<App bridge={api} />)
    await waitFor(() => expect(api.fetchSnapshot).toHaveBeenCalled())
    notify(true)
    const loading = await screen.findByText('正在读取玩家状态…')
    expect(loading.closest('main')).toHaveAttribute('data-tauri-drag-region', 'deep')
    expect(screen.getByText('拖动调整位置')).toBeInTheDocument()
  })

  it('starts in adjustment UI when the loaded config is unlocked', async () => {
    const api = bridge({
      loadConfig: vi.fn(async () => ({ ...config, locked: false })),
      fetchSnapshot: vi.fn<DesktopBridge['fetchSnapshot']>(() => new Promise(() => {})),
    })
    render(<App bridge={api} />)
    await waitFor(() => expect(api.fetchSnapshot).toHaveBeenCalled())
    expect(await screen.findByText('拖动调整位置')).toBeInTheDocument()
  })
})
