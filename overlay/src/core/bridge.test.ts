import { beforeEach, describe, expect, it, vi } from 'vitest'

const tauri = vi.hoisted(() => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => true),
}))
const events = vi.hoisted(() => ({ listen: vi.fn() }))

vi.mock('@tauri-apps/api/core', () => tauri)
vi.mock('@tauri-apps/api/event', () => events)

import { createDesktopBridge } from './bridge'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

const snapshotRequest = { baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'uid' }

describe('native HTTP invoke gate', () => {
  beforeEach(() => {
    tauri.invoke.mockReset()
    tauri.isTauri.mockReturnValue(true)
    events.listen.mockReset()
  })

  it('subscribes to native lifecycle events and returns their cleanup handles', async () => {
    const unlistenAdjustment = vi.fn()
    const unlistenReselect = vi.fn()
    events.listen.mockResolvedValueOnce(unlistenAdjustment).mockResolvedValueOnce(unlistenReselect)
    const bridge = createDesktopBridge()
    const adjustment = vi.fn()
    const reselect = vi.fn()

    await expect(bridge.onAdjustmentModeChanged!(adjustment)).resolves.toBe(unlistenAdjustment)
    await expect(bridge.onReselectPlayer!(reselect)).resolves.toBe(unlistenReselect)
    events.listen.mock.calls[0][1]({ payload: true })
    events.listen.mock.calls[1][1]({ payload: null })
    expect(adjustment).toHaveBeenCalledWith(true)
    expect(reselect).toHaveBeenCalledTimes(1)
  })

  it('rejects an aborted caller promptly but waits for its native invoke before starting the next HTTP request', async () => {
    const firstNative = deferred<unknown>()
    tauri.invoke.mockImplementationOnce(() => firstNative.promise).mockResolvedValueOnce([])
    const bridge = createDesktopBridge()
    const firstController = new AbortController()
    const secondController = new AbortController()

    const first = bridge.fetchSnapshot(snapshotRequest, firstController.signal)
    await vi.waitFor(() => expect(tauri.invoke).toHaveBeenCalledTimes(1))
    firstController.abort()
    await expect(first).rejects.toMatchObject({ name: 'AbortError' })

    const second = bridge.listPlayers('https://palbox.test', secondController.signal)
    await Promise.resolve()
    expect(tauri.invoke).toHaveBeenCalledTimes(1)

    firstNative.resolve({ status: 304 })
    await expect(second).resolves.toEqual([])
    expect(tauri.invoke).toHaveBeenNthCalledWith(2, 'list_players', { baseUrl: 'https://palbox.test' })
  })

  it('does not invoke a queued HTTP request that is aborted before its turn', async () => {
    const firstNative = deferred<unknown>()
    tauri.invoke.mockImplementationOnce(() => firstNative.promise)
    const bridge = createDesktopBridge()
    const queuedController = new AbortController()

    const first = bridge.fetchSnapshot(snapshotRequest, new AbortController().signal)
    await vi.waitFor(() => expect(tauri.invoke).toHaveBeenCalledTimes(1))
    const queued = bridge.listPlayers('https://palbox.test', queuedController.signal)
    queuedController.abort()
    await expect(queued).rejects.toMatchObject({ name: 'AbortError' })

    firstNative.resolve({ status: 304 })
    await expect(first).resolves.toEqual({ status: 304 })
    await Promise.resolve()
    expect(tauri.invoke).toHaveBeenCalledTimes(1)
  })

  it('does not serialize non-HTTP commands behind the HTTP gate', async () => {
    const native = deferred<unknown>()
    tauri.invoke.mockImplementationOnce(() => native.promise).mockResolvedValueOnce('settings')
    const bridge = createDesktopBridge()
    void bridge.fetchSnapshot(snapshotRequest, new AbortController().signal)
    await vi.waitFor(() => expect(tauri.invoke).toHaveBeenCalledTimes(1))

    await expect(bridge.currentWindowLabel()).resolves.toBe('settings')
    expect(tauri.invoke).toHaveBeenCalledTimes(2)
    native.resolve({ status: 304 })
  })
})
