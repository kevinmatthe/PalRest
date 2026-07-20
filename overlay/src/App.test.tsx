import { readFileSync } from 'node:fs'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { Presentation } from './contracts/presentation'
import type { DesktopBridge, FetchPresentationResult } from './core/bridge'
import { cloneLayoutProfile, PALWORLD_DEFAULT_LAYOUT } from './core/layout'
import type { OverlayConfigV2 } from './core/config'
import App from './App'

afterEach(cleanup)

function config(overrides: Partial<OverlayConfigV2> = {}): OverlayConfigV2 {
  return {
    schema: 2, baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'uid',
    scale: 1, locked: true,
    layouts: { palworld: cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT) },
    ...overrides,
  }
}

function presentation(userId = 'uid', fields: Presentation['fields'] = []): Presentation {
  return {
    schema: 'overlay.presentation/v1', game_id: 'palworld', user_id: userId,
    observed_at: '2026-07-16T12:00:00Z', fresh_until: '2099-07-16T12:00:00Z',
    source_status: 'online', identity: { display_name: `Player ${userId}`, level: 12 }, fields,
  }
}

function bridge(overrides: Partial<DesktopBridge> = {}): DesktopBridge {
  return {
    fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>(async () => ({ status: 503, code: 'presentation_unavailable' })),
    loadConfig: vi.fn(async () => config()), saveConfig: vi.fn(), listPlayers: vi.fn(async () => []),
    currentWindowLabel: vi.fn(async () => 'overlay' as const), setAdjustmentMode: vi.fn(),
    ...overrides,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

describe('App presentation overlay', () => {
  it('loads the window label and config concurrently before routing to settings', async () => {
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const),
      loadConfig: vi.fn(async () => null),
    })
    render(<App bridge={api} />)

    expect(api.currentWindowLabel).toHaveBeenCalledTimes(1)
    expect(api.loadConfig).toHaveBeenCalledTimes(1)
    expect(screen.getByText('正在读取本地设置…')).toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: '悬浮条设置' })).toBeInTheDocument()
  })

  it('selects an exact detected Windows UID after the player list loads', async () => {
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

  it('does not guess a missing Windows UID from player names', async () => {
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const),
      loadConfig: vi.fn(async () => null),
      currentPlatform: vi.fn(async () => 'windows'),
      detectedPalworldUserId: vi.fn(async () => 'steam_42'),
      listPlayers: vi.fn(async () => [{ user_id: 'other', name: 'steam_42', account_name: 'steam_42' }]),
    })
    render(<App bridge={api} />)
    await screen.findByRole('heading', { name: '悬浮条设置' })
    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'https://palbox.test' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.listPlayers).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText('玩家')).toHaveValue('')
  })

  it('keeps macOS player selection manual and skips Windows UID detection', async () => {
    const detectedPalworldUserId = vi.fn(async () => 'steam_42')
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const),
      loadConfig: vi.fn(async () => null),
      currentPlatform: vi.fn(async () => 'macos'),
      detectedPalworldUserId,
      listPlayers: vi.fn(async () => [{ user_id: 'steam_42', name: 'Lamball', account_name: '' }]),
    })
    render(<App bridge={api} />)
    await screen.findByRole('heading', { name: '悬浮条设置' })
    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'https://palbox.test' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.listPlayers).toHaveBeenCalledTimes(1))
    expect(detectedPalworldUserId).not.toHaveBeenCalled()
    expect(screen.getByLabelText('玩家')).toHaveValue('')
  })

  it('does not start detected-user work after unmount while platform detection is pending', async () => {
    const platform = deferred<string>()
    const detectedPalworldUserId = vi.fn(async () => 'steam_42')
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const),
      loadConfig: vi.fn(async () => null),
      currentPlatform: vi.fn(() => platform.promise),
      detectedPalworldUserId,
    })
    const { unmount } = render(<App bridge={api} />)
    await waitFor(() => expect(api.currentPlatform).toHaveBeenCalledTimes(1))
    unmount()
    platform.resolve('windows')
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

  it('opens settings once and keeps first-run UI compact in the overlay window', async () => {
    const openSettings = vi.fn(async () => {})
    const api = bridge({ loadConfig: vi.fn(async () => null), openSettings })
    render(<App bridge={api} />)
    expect(await screen.findByText('需要先完成设置')).toBeInTheDocument()
    await waitFor(() => expect(openSettings).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('heading', { name: '悬浮条设置' })).not.toBeInTheDocument()
  })

  it('polls the current presentation request and renders the configured layout', async () => {
    const fields: Presentation['fields'] = [{
      id: 'network.latency', label: '延迟', kind: 'latency_ms', available: true,
      value: 28, tone: 'normal',
    }]
    const fetchPresentation = vi.fn<NonNullable<DesktopBridge['fetchPresentation']>>(async () => ({
      status: 200, body: presentation('uid', fields),
    }))
    const api = bridge({ fetchPresentation })
    render(<App bridge={api} />)

    expect(await screen.findByTestId('identity-header')).toHaveTextContent('Player uid')
    expect(fetchPresentation).toHaveBeenCalledWith(
      { baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'uid' },
      expect.any(AbortSignal),
    )
    const slots = within(screen.getByRole('list', { name: '玩家状态字段' })).getAllByRole('listitem')
    expect(slots).toHaveLength(4)
    expect(within(slots[0]).getByRole('definition')).toHaveTextContent('28 ms')
  })

  it('hot-swaps both the request and slot ordering without reloading the page', async () => {
    let publishConfig!: (value: unknown) => void
    const oldRequest = deferred<FetchPresentationResult>()
    const fetchPresentation = vi.fn<NonNullable<DesktopBridge['fetchPresentation']>>((request) => {
      if (request.userId === 'uid') return oldRequest.promise
      return Promise.resolve({ status: 200, body: presentation('uid-2', [
        { id: 'custom.first', label: '自定义首槽', kind: 'text', available: true, value: '新布局', tone: 'warning' },
      ]) })
    })
    const api = bridge({
      fetchPresentation,
      onConfigChanged: vi.fn(async (handler) => { publishConfig = handler; return () => {} }),
    })
    render(<App bridge={api} />)
    await waitFor(() => expect(fetchPresentation).toHaveBeenCalledTimes(1))
    const oldSignal = fetchPresentation.mock.calls[0][1]

    const replacement = config({ userId: 'uid-2', baseUrl: 'https://replacement.test' })
    replacement.layouts.palworld.slots[0] = { primary: 'custom.first', fallback: 'network.latency' }
    act(() => publishConfig(replacement))
    oldRequest.resolve({ status: 200, body: presentation('uid') })

    expect(await screen.findByTestId('identity-header')).toHaveTextContent('Player uid-2')
    expect(oldSignal.aborted).toBe(true)
    expect(fetchPresentation).toHaveBeenCalledWith(
      { baseUrl: 'https://replacement.test', gameId: 'palworld', userId: 'uid-2' },
      expect.any(AbortSignal),
    )
    expect(screen.getByRole('listitem', { name: '自定义首槽 新布局' })).toBeInTheDocument()
  })

  it('registers the config listener before bootstrap and uses an early valid config event', async () => {
    const registration = deferred<() => void>()
    const loadConfig = vi.fn(async () => config())
    let publishConfig!: (value: unknown) => void
    const fetchPresentation = vi.fn<NonNullable<DesktopBridge['fetchPresentation']>>(() => new Promise(() => {}))
    const api = bridge({
      loadConfig, fetchPresentation,
      onConfigChanged: vi.fn((handler) => { publishConfig = handler; return registration.promise }),
    })
    render(<App bridge={api} />)
    await waitFor(() => expect(api.onConfigChanged).toHaveBeenCalledTimes(1))
    expect(loadConfig).not.toHaveBeenCalled()

    act(() => publishConfig(config({ userId: 'event-user' })))
    registration.resolve(() => {})

    await waitFor(() => expect(fetchPresentation).toHaveBeenCalledWith(
      { baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'event-user' },
      expect.any(AbortSignal),
    ))
    expect(fetchPresentation).not.toHaveBeenCalledWith(
      expect.objectContaining({ userId: 'uid' }), expect.any(AbortSignal),
    )
  })

  it('aborts an in-flight presentation request on unmount', async () => {
    const fetchPresentation = vi.fn<NonNullable<DesktopBridge['fetchPresentation']>>(() => new Promise(() => {}))
    const api = bridge({ fetchPresentation })
    const { unmount } = render(<App bridge={api} />)
    await waitFor(() => expect(fetchPresentation).toHaveBeenCalledTimes(1))
    const signal = fetchPresentation.mock.calls[0][1]
    unmount()
    expect(signal.aborted).toBe(true)
  })

  it('reflects adjustment events and cleans up a resolved native listener', async () => {
    let notify!: (enabled: boolean) => void
    const unlisten = vi.fn()
    const api = bridge({
      fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>(async () => ({ status: 200, body: presentation() })),
      onAdjustmentModeChanged: vi.fn(async (handler) => { notify = handler; return unlisten }),
    })
    const { unmount } = render(<App bridge={api} />)
    await screen.findByTestId('identity-header')
    act(() => notify(true))
    expect(await screen.findByText('拖动调整位置')).toBeInTheDocument()
    act(() => notify(false))
    await waitFor(() => expect(screen.queryByText('拖动调整位置')).not.toBeInTheDocument())
    unmount()
    expect(unlisten).toHaveBeenCalledTimes(1)
  })

  it('cleans up an adjustment listener that resolves after unmount', async () => {
    const registration = deferred<() => void>()
    const unlisten = vi.fn()
    const api = bridge({ onAdjustmentModeChanged: vi.fn(() => registration.promise) })
    const { unmount } = render(<App bridge={api} />)
    unmount()
    registration.resolve(unlisten)
    await waitFor(() => expect(unlisten).toHaveBeenCalledTimes(1))
  })

  it('routes reselect events into settings without restoring the saved UID', async () => {
    let reselect!: () => void
    const unlisten = vi.fn()
    const listPlayers = vi.fn(async () => [{ user_id: 'uid', name: 'Player', account_name: '' }])
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const),
      onReselectPlayer: vi.fn(async (handler) => { reselect = handler; return unlisten }),
      listPlayers,
    })
    const { unmount } = render(<App bridge={api} />)
    await screen.findByRole('heading', { name: '悬浮条设置' })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid'))
    act(() => reselect())
    await waitFor(() => expect(listPlayers).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue(''))
    unmount()
    expect(unlisten).toHaveBeenCalledTimes(1)
  })

  it('keeps loading and safe failures compact and draggable in adjustment mode', async () => {
    let notify!: (enabled: boolean) => void
    const api = bridge({
      fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>(() => new Promise(() => {})),
      onAdjustmentModeChanged: vi.fn(async (handler) => { notify = handler; return () => {} }),
    })
    render(<App bridge={api} />)
    await waitFor(() => expect(api.fetchPresentation).toHaveBeenCalled())
    act(() => notify(true))
    const loading = await screen.findByText('正在读取玩家状态…')
    expect(loading.closest('main')).toHaveAttribute('data-tauri-drag-region', 'deep')
    expect(screen.getByText('拖动调整位置')).toBeInTheDocument()
  })

  it('opens settings for a missing player and does not leak failure details', async () => {
    const openSettings = vi.fn(async () => {})
    const api = bridge({
      openSettings,
      fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>(async () => ({ status: 404, code: 'player_not_found' })),
    })
    render(<App bridge={api} />)
    expect(await screen.findByText('玩家已失效，请在设置中重新选择')).toBeInTheDocument()
    await waitFor(() => expect(openSettings).toHaveBeenCalledTimes(1))
  })

  it('recovers from a terminal presentation after hot config changes request identity', async () => {
    let publishConfig!: (value: unknown) => void
    const fetchPresentation = vi.fn<DesktopBridge['fetchPresentation']>()
      .mockResolvedValueOnce({ status: 404, code: 'player_not_found' })
      .mockResolvedValueOnce({ status: 200, body: presentation('replacement-user') })
    const api = bridge({
      fetchPresentation,
      onConfigChanged: vi.fn(async (handler) => { publishConfig = handler; return () => {} }),
    })
    render(<App bridge={api} />)
    expect(await screen.findByText('玩家已失效，请在设置中重新选择')).toBeInTheDocument()

    act(() => publishConfig(config({ userId: 'replacement-user' })))

    expect(await screen.findByTestId('identity-header')).toHaveTextContent('Player replacement-user')
    expect(fetchPresentation).toHaveBeenNthCalledWith(2, {
      baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'replacement-user',
    }, expect.any(AbortSignal))
  })

  it('renders a compact disconnected terminal state without last-good data', async () => {
    const api = bridge({
      fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>(async () => ({ status: 503, code: 'presentation_unavailable' })),
    })
    render(<App bridge={api} />)
    expect(await screen.findByText('暂时无法连接服务')).toBeInTheDocument()
    expect(screen.queryByTestId('identity-header')).not.toBeInTheDocument()
  })

  it('renders a compact incompatible terminal state without last-good data', async () => {
    const api = bridge({
      fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>(async () => ({ status: 404, code: 'game_not_supported' })),
    })
    render(<App bridge={api} />)
    expect(await screen.findByText('服务版本不兼容，请更新应用')).toBeInTheDocument()
    expect(screen.queryByTestId('identity-header')).not.toBeInTheDocument()
  })

  it('renders a compact bootstrap error without leaking loadConfig details', async () => {
    const api = bridge({ loadConfig: vi.fn(async () => { throw new Error('secret path') }) })
    render(<App bridge={api} />)
    expect(await screen.findByText('无法读取悬浮条设置')).toBeInTheDocument()
    expect(screen.queryByText(/secret path/)).not.toBeInTheDocument()
  })

  it('ignores invalid config events without aborting or replacing the current request', async () => {
    let publishConfig!: (value: unknown) => void
    const fetchPresentation = vi.fn<DesktopBridge['fetchPresentation']>(() => new Promise(() => {}))
    const api = bridge({
      fetchPresentation,
      onConfigChanged: vi.fn(async (handler) => { publishConfig = handler; return () => {} }),
    })
    render(<App bridge={api} />)
    await waitFor(() => expect(fetchPresentation).toHaveBeenCalledTimes(1))
    const signal = fetchPresentation.mock.calls[0][1]
    act(() => publishConfig({ ...config(), userId: '' }))
    await Promise.resolve()
    expect(fetchPresentation).toHaveBeenCalledTimes(1)
    expect(signal.aborted).toBe(false)
  })

  it('cleans up a resolved config listener on unmount', async () => {
    const unlisten = vi.fn()
    const api = bridge({
      fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>(() => new Promise(() => {})),
      onConfigChanged: vi.fn(async () => unlisten),
    })
    const { unmount } = render(<App bridge={api} />)
    await waitFor(() => expect(api.onConfigChanged).toHaveBeenCalledTimes(1))
    unmount()
    expect(unlisten).toHaveBeenCalledTimes(1)
  })

  it('cleans up a config listener that resolves after unmount', async () => {
    const registration = deferred<() => void>()
    const unlisten = vi.fn()
    const api = bridge({ onConfigChanged: vi.fn(() => registration.promise) })
    const { unmount } = render(<App bridge={api} />)
    unmount()
    registration.resolve(unlisten)
    await waitFor(() => expect(unlisten).toHaveBeenCalledTimes(1))
  })

  it('does not apply overlay config events to settings-window bootstrap', async () => {
    let publishConfig!: (value: unknown) => void
    const api = bridge({
      currentWindowLabel: vi.fn(async () => 'settings' as const),
      loadConfig: vi.fn(async () => null),
      onConfigChanged: vi.fn(async (handler) => { publishConfig = handler; return () => {} }),
    })
    render(<App bridge={api} />)
    await screen.findByRole('heading', { name: '悬浮条设置' })
    act(() => publishConfig(config({ userId: 'replacement' })))
    expect(screen.queryByRole('button', { name: '调整悬浮条位置' })).not.toBeInTheDocument()
  })

  it('starts in adjustment UI when the loaded config is unlocked', async () => {
    const api = bridge({
      loadConfig: vi.fn(async () => config({ locked: false })),
      fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>(() => new Promise(() => {})),
    })
    render(<App bridge={api} />)
    await waitFor(() => expect(api.fetchPresentation).toHaveBeenCalledTimes(1))
    expect(await screen.findByText('拖动调整位置')).toBeInTheDocument()
  })
})
