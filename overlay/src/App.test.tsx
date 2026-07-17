import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
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
})
