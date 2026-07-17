import { beforeEach, describe, expect, it, vi } from 'vitest'

const tauri = vi.hoisted(() => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => true),
}))
const events = vi.hoisted(() => ({ listen: vi.fn() }))

vi.mock('@tauri-apps/api/core', () => tauri)
vi.mock('@tauri-apps/api/event', () => events)

import { createBrowserPlaceholderBridge, createDesktopBridge } from './bridge'

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
const presentationRequest = { ...snapshotRequest, etag: '"presentation-v1"' }

describe('native HTTP invoke gate', () => {
  beforeEach(() => {
    tauri.invoke.mockReset()
    tauri.isTauri.mockReturnValue(true)
    events.listen.mockReset()
  })

  it('subscribes to native lifecycle events and returns their cleanup handles', async () => {
    const unlistenAdjustment = vi.fn()
    const unlistenReselect = vi.fn()
    const unlistenConfig = vi.fn()
    events.listen
      .mockResolvedValueOnce(unlistenAdjustment)
      .mockResolvedValueOnce(unlistenReselect)
      .mockResolvedValueOnce(unlistenConfig)
    const bridge = createDesktopBridge()
    const adjustment = vi.fn()
    const reselect = vi.fn()
    const config = vi.fn()

    await expect(bridge.onAdjustmentModeChanged!(adjustment)).resolves.toBe(unlistenAdjustment)
    await expect(bridge.onReselectPlayer!(reselect)).resolves.toBe(unlistenReselect)
    await expect(bridge.onConfigChanged!(config)).resolves.toBe(unlistenConfig)
    events.listen.mock.calls[0][1]({ payload: true })
    events.listen.mock.calls[1][1]({ payload: null })
    const payload = { schema: 1, userId: 'replacement' }
    events.listen.mock.calls[2][1]({ payload })
    expect(adjustment).toHaveBeenCalledWith(true)
    expect(reselect).toHaveBeenCalledTimes(1)
    expect(events.listen).toHaveBeenNthCalledWith(3, 'overlay-config-changed', expect.any(Function))
    expect(config).toHaveBeenCalledWith(payload)
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

  it('maps presentation requests and results exactly through the serialized HTTP gate', async () => {
    tauri.invoke.mockResolvedValueOnce({
      status: 200,
      etag: '"presentation-v2"',
      body: { schema: 'overlay.presentation/v1' },
    })
    const bridge = createDesktopBridge()

    await expect(bridge.fetchPresentation(
      presentationRequest,
      new AbortController().signal,
    )).resolves.toEqual({
      status: 200,
      etag: '"presentation-v2"',
      body: { schema: 'overlay.presentation/v1' },
    })
    expect(tauri.invoke).toHaveBeenCalledWith('fetch_presentation', {
      request: presentationRequest,
    })
  })

  it('handles presentation aborts before invocation and after invocation starts', async () => {
    const bridge = createDesktopBridge()
    const before = new AbortController()
    before.abort()
    await expect(bridge.fetchPresentation(presentationRequest, before.signal))
      .rejects.toMatchObject({ name: 'AbortError' })
    expect(tauri.invoke).not.toHaveBeenCalled()

    const native = deferred<unknown>()
    tauri.invoke.mockImplementationOnce(() => native.promise)
    const after = new AbortController()
    const request = bridge.fetchPresentation(presentationRequest, after.signal)
    await vi.waitFor(() => expect(tauri.invoke).toHaveBeenCalledTimes(1))
    after.abort()
    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    native.resolve({ status: 304 })
  })

  it('maps a persisted native save rejection without leaking its raw value', async () => {
    const nativeFailure = { persisted: true, secret: 'native details' }
    tauri.invoke.mockRejectedValueOnce(nativeFailure)
    const bridge = createDesktopBridge()
    let caught: unknown

    try {
      await bridge.saveConfig({
        schema: 1, baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'uid', scale: 1, locked: true,
      })
    } catch (error) {
      caught = error
    }
    expect(caught).toMatchObject({ name: 'ConfigSaveError', persisted: true })
    expect(caught).not.toBe(nativeFailure)
    expect(String(caught)).not.toContain('native details')
  })
})

describe('browser placeholder bridge', () => {
  it('provides a no-op config listener cleanup', async () => {
    const bridge = createBrowserPlaceholderBridge()
    const handler = vi.fn()

    const unlisten = await bridge.onConfigChanged!(handler)

    expect(unlisten).toBeTypeOf('function')
    expect(() => unlisten()).not.toThrow()
    expect(handler).not.toHaveBeenCalled()
  })
})
