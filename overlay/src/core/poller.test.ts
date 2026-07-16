import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type {
  FetchSnapshotRequest,
  FetchSnapshotResult,
  OverlayBridge,
} from './bridge'
import { SnapshotPoller } from './poller'

const config = {
  baseUrl: 'http://127.0.0.1:8212/api',
  gameId: 'palworld',
  userId: 'steam-42',
}

function snapshot(freshUntil = '2026-07-16T12:00:10.000Z') {
  return {
    schema: 'overlay.snapshot/v1',
    game_id: config.gameId,
    user_id: config.userId,
    observed_at: '2026-07-16T12:00:00.000Z',
    fresh_until: freshUntil,
    source_status: 'online',
    capabilities: ['identity'],
    identity: { display_name: 'Lamball' },
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((yes, no) => {
    resolve = yes
    reject = no
  })
  return { promise, resolve, reject }
}

function bridgeWith(
  implementation: OverlayBridge['fetchSnapshot'],
): OverlayBridge & { fetchSnapshot: ReturnType<typeof vi.fn<OverlayBridge['fetchSnapshot']>> } {
  return { fetchSnapshot: vi.fn(implementation) }
}

async function settle() {
  await Promise.resolve()
  await Promise.resolve()
}

describe('SnapshotPoller', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime('2026-07-16T12:00:00.000Z')
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('transitions idle -> loading -> ready and requests the configured player immediately', async () => {
    const pending = deferred<FetchSnapshotResult>()
    const bridge = bridgeWith(() => pending.promise)
    const poller = new SnapshotPoller({ bridge, config, now: () => Date.now() })
    const states = [poller.getState().status]
    poller.subscribe(() => states.push(poller.getState().status))

    poller.start()
    expect(states).toEqual(['idle', 'loading'])
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(1)
    const [request, signal] = bridge.fetchSnapshot.mock.calls[0]
    expect(request).toEqual(config)
    expect(signal.aborted).toBe(false)

    pending.resolve({ status: 200, etag: 'v1', body: snapshot() })
    await settle()
    expect(states).toEqual(['idle', 'loading', 'ready'])
    expect(poller.getState()).toMatchObject({ status: 'ready', snapshot: snapshot() })
  })

  it('keeps the same snapshot on 304 and reevaluates its freshness', async () => {
    const bridge = bridgeWith(vi.fn()
      .mockResolvedValueOnce({ status: 200, etag: 'v1', body: snapshot() })
      .mockResolvedValueOnce({ status: 304 }))
    const poller = new SnapshotPoller({ bridge, config, now: () => Date.now() })
    poller.start()
    await settle()
    const first = poller.getState()
    expect(first.status).toBe('ready')
    const firstSnapshot = first.status === 'ready' ? first.snapshot : undefined

    await vi.advanceTimersByTimeAsync(2_000)
    const afterNotModified = poller.getState()
    expect(afterNotModified).toEqual({ status: 'ready', snapshot: firstSnapshot })
    expect(afterNotModified.status === 'ready' && afterNotModified.snapshot).toBe(firstSnapshot)
    expect(bridge.fetchSnapshot.mock.calls[1][0]).toMatchObject({ etag: 'v1' })
  })

  it('retains the last valid snapshot when a network error disconnects it', async () => {
    const bridge = bridgeWith(vi.fn()
      .mockResolvedValueOnce({ status: 200, body: snapshot() })
      .mockRejectedValueOnce(new Error('offline')))
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()
    const ready = poller.getState()
    const valid = ready.status === 'ready' ? ready.snapshot : undefined

    await vi.advanceTimersByTimeAsync(2_000)
    expect(poller.getState()).toEqual({ status: 'disconnected', snapshot: valid })
  })

  it('recovers from disconnected and resets the polling delay to 2000ms', async () => {
    const bridge = bridgeWith(vi.fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ status: 200, body: snapshot() })
      .mockResolvedValue({ status: 304 }))
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()
    expect(poller.getState().status).toBe('disconnected')

    await vi.advanceTimersByTimeAsync(2_000)
    expect(poller.getState().status).toBe('ready')
    await vi.advanceTimersByTimeAsync(1_999)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(3)
  })

  it('uses retry delays 2000, 4000, 8000, 16000, 30000 and caps at 30000ms', async () => {
    const callTimes: number[] = []
    const bridge = bridgeWith(async () => {
      callTimes.push(Date.now())
      throw new Error('offline')
    })
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()

    for (const [index, delay] of [2_000, 4_000, 8_000, 16_000, 30_000, 30_000].entries()) {
      await vi.advanceTimersByTimeAsync(delay - 1)
      expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(index + 1)
      await vi.advanceTimersByTimeAsync(1)
    }
    expect(callTimes.map((time, index) => index === 0 ? 0 : time - callTimes[index - 1]))
      .toEqual([0, 2_000, 4_000, 8_000, 16_000, 30_000, 30_000])
  })

  it('marks a snapshot stale at the freshness deadline while the next request is hung', async () => {
    const hung = deferred<FetchSnapshotResult>()
    const bridge = bridgeWith(vi.fn()
      .mockResolvedValueOnce({ status: 200, body: snapshot('2026-07-16T12:00:02.000Z') })
      .mockImplementationOnce(() => hung.promise))
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()

    await vi.advanceTimersByTimeAsync(2_000)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(2)
    expect(poller.getState().status).toBe('ready')
    await vi.advanceTimersByTimeAsync(1)
    expect(poller.getState().status).toBe('stale')
  })

  it('enters needs-player without retrying when the player is not found', async () => {
    const bridge = bridgeWith(async () => ({ status: 404, code: 'player_not_found' }))
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()
    expect(poller.getState()).toEqual({ status: 'needs-player', code: 'player_not_found' })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(1)
  })

  it.each([
    [{ status: 404, code: 'game_not_supported' } as const, 'unsupported game'],
    [{ status: 200, body: { schema: 'overlay.snapshot/v2' } } as const, 'unsupported schema'],
  ])('enters incompatible for %s (%s)', async (result, _label) => {
    const bridge = bridgeWith(async () => result)
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()
    expect(poller.getState().status).toBe('incompatible')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(1)
  })

  it('stop aborts the request, clears every timer, and ignores late completion', async () => {
    const pending = deferred<FetchSnapshotResult>()
    const bridge = bridgeWith(() => pending.promise)
    const poller = new SnapshotPoller({ bridge, config })
    const listener = vi.fn()
    poller.subscribe(listener)
    poller.start()
    const signal = bridge.fetchSnapshot.mock.calls[0][1]

    poller.stop()
    expect(signal.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
    expect(poller.getState()).toEqual({ status: 'idle' })
    const callsAfterStop = listener.mock.calls.length
    pending.resolve({ status: 200, body: snapshot() })
    await settle()
    expect(listener).toHaveBeenCalledTimes(callsAfterStop)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(1)
  })

  it('keeps ownership until an abort-ignoring request settles and publishes timeout failure once', async () => {
    const first = deferred<FetchSnapshotResult>()
    let active = 0
    let maxActive = 0
    const bridge = bridgeWith(vi.fn().mockImplementationOnce(async (
      _request: FetchSnapshotRequest,
      _signal: AbortSignal,
    ) => {
      active += 1
      maxActive = Math.max(maxActive, active)
      try {
        return await first.promise
      } finally {
        active -= 1
      }
    }).mockImplementationOnce(async () => {
      active += 1
      maxActive = Math.max(maxActive, active)
      active -= 1
      return { status: 200, body: snapshot('2026-07-16T12:02:00.000Z') }
    }))
    const poller = new SnapshotPoller({ bridge, config })
    const states: string[] = []
    poller.subscribe(() => states.push(poller.getState().status))
    poller.start()

    await vi.advanceTimersByTimeAsync(4_999)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(bridge.fetchSnapshot.mock.calls[0][1].aborted).toBe(true)
    expect(poller.getState().status).toBe('disconnected')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(1)
    expect(active).toBe(1)

    first.resolve({ status: 200, body: snapshot() })
    await settle()
    await vi.advanceTimersByTimeAsync(0)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(2)
    expect(maxActive).toBe(1)
    expect(poller.getState().status).toBe('ready')
    expect(states.filter((status) => status === 'disconnected')).toHaveLength(1)
  })

  it('stays stopped when a timed-out request settles late and clears every timer', async () => {
    const first = deferred<FetchSnapshotResult>()
    const bridge = bridgeWith(() => first.promise)
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await vi.advanceTimersByTimeAsync(5_000)
    poller.stop()

    first.resolve({ status: 200, body: snapshot() })
    await settle()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(poller.getState()).toEqual({ status: 'idle' })
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each([
    ['user_id', { user_id: 'steam-attacker' }],
    ['game_id', { game_id: 'other-game' }],
  ])('rejects a parsed snapshot whose %s differs from the request identity', async (field, change) => {
    const bridge = bridgeWith(vi.fn()
      .mockResolvedValueOnce({ status: 200, etag: 'trusted', body: snapshot() })
      .mockResolvedValueOnce({
        status: 200,
        etag: 'untrusted',
        body: { ...snapshot(), ...change },
      })
      .mockResolvedValueOnce({ status: 304 }))
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()
    const trusted = poller.getState()

    await vi.advanceTimersByTimeAsync(2_000)
    expect(poller.getState()).toEqual({
      status: 'incompatible',
      reason: `${field} does not match request`,
    })
    poller.start()
    await settle()
    expect(bridge.fetchSnapshot.mock.calls[2][0].etag).toBe('trusted')
    expect(poller.getState()).toEqual(trusted)
  })

  it('treats malformed current-schema bodies as transient and recovers with the last snapshot retained', async () => {
    const bridge = bridgeWith(vi.fn()
      .mockResolvedValueOnce({ status: 200, body: snapshot() })
      .mockResolvedValueOnce({ status: 200, body: { schema: 'overlay.snapshot/v1' } })
      .mockResolvedValueOnce({ status: 200, body: snapshot() }))
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()
    const ready = poller.getState()
    const valid = ready.status === 'ready' ? ready.snapshot : undefined

    await vi.advanceTimersByTimeAsync(2_000)
    expect(poller.getState()).toEqual({ status: 'disconnected', snapshot: valid })
    await vi.advanceTimersByTimeAsync(2_000)
    expect(poller.getState().status).toBe('ready')
  })

  it.each([null, {}, { schema: 42 }])(
    'treats a body without an unsupported schema string as transient: %s',
    async (body) => {
      const bridge = bridgeWith(async () => ({ status: 200, body }))
      const poller = new SnapshotPoller({ bridge, config })
      poller.start()
      await settle()
      expect(poller.getState().status).toBe('disconnected')
      await vi.advanceTimersByTimeAsync(2_000)
      expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(2)
    },
  )

  it('chunks very distant freshness deadlines without marking the snapshot stale early', async () => {
    const maxTimerDelay = 2_147_483_647
    const expiresAt = Date.now() + maxTimerDelay + 1_000
    const hung = deferred<FetchSnapshotResult>()
    const bridge = bridgeWith(vi.fn()
      .mockResolvedValueOnce({ status: 200, body: snapshot(new Date(expiresAt).toISOString()) })
      .mockImplementationOnce(() => hung.promise))
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()

    await vi.advanceTimersByTimeAsync(maxTimerDelay)
    expect(poller.getState().status).toBe('disconnected')
    await vi.advanceTimersByTimeAsync(1_000)
    expect(poller.getState().status).toBe('disconnected')
    await vi.advanceTimersByTimeAsync(1)
    expect(poller.getState().status).toBe('stale')
  })

  it('publishes an already expired snapshot as stale without an intermediate ready state', async () => {
    const bridge = bridgeWith(async () => ({
      status: 200,
      body: snapshot('2026-07-16T11:59:59.000Z'),
    }))
    const poller = new SnapshotPoller({ bridge, config })
    const states: string[] = []
    poller.subscribe(() => states.push(poller.getState().status))
    poller.start()
    await settle()

    expect(states).toEqual(['loading', 'stale'])
  })

  it('exposes bound getState and subscribe callbacks for React external stores', async () => {
    const bridge = bridgeWith(async () => ({ status: 200, body: snapshot() }))
    const poller = new SnapshotPoller({ bridge, config })
    const getState = poller.getState
    const subscribe = poller.subscribe
    const listener = vi.fn()
    const unsubscribe = subscribe(listener)

    expect(getState()).toEqual({ status: 'idle' })
    poller.start()
    await settle()
    expect(listener).toHaveBeenCalled()
    expect(getState().status).toBe('ready')
    unsubscribe()
  })

  it('does not let a reentrant old generation increase the new generation retry delay', async () => {
    const bridge = bridgeWith(vi.fn()
      .mockRejectedValueOnce(new Error('old failure'))
      .mockRejectedValueOnce(new Error('new failure'))
      .mockResolvedValueOnce({ status: 200, body: snapshot() }))
    const poller = new SnapshotPoller({ bridge, config })
    let restarted = false
    poller.subscribe(() => {
      if (!restarted && poller.getState().status === 'disconnected') {
        restarted = true
        poller.stop()
        poller.start()
      }
    })
    poller.start()
    await settle()

    await vi.advanceTimersByTimeAsync(1_999)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(3)
  })

  it('sends and updates ETags, clearing one when a successful 200 omits it', async () => {
    const bridge = bridgeWith(vi.fn()
      .mockResolvedValueOnce({ status: 200, etag: 'v1', body: snapshot() })
      .mockResolvedValueOnce({ status: 304 })
      .mockResolvedValueOnce({ status: 200, etag: 'v2', body: snapshot() })
      .mockResolvedValueOnce({ status: 200, body: snapshot() })
      .mockResolvedValueOnce({ status: 304 }))
    const poller = new SnapshotPoller({ bridge, config })
    poller.start()
    await settle()
    for (let index = 0; index < 4; index += 1) {
      await vi.advanceTimersByTimeAsync(2_000)
    }
    expect(bridge.fetchSnapshot.mock.calls.map(([request]) => request.etag))
      .toEqual([undefined, 'v1', 'v1', 'v2', undefined])
    for (const [request] of bridge.fetchSnapshot.mock.calls) {
      expect(request).toMatchObject(config)
    }
  })

  it('isolates listener errors from state publication and scheduling', async () => {
    const bridge = bridgeWith(vi.fn()
      .mockResolvedValueOnce({ status: 200, body: snapshot() })
      .mockResolvedValueOnce({ status: 304 }))
    const poller = new SnapshotPoller({ bridge, config })
    poller.subscribe(() => { throw new Error('bad listener') })
    poller.start()
    await settle()
    expect(poller.getState().status).toBe('ready')
    await vi.advanceTimersByTimeAsync(2_000)
    expect(bridge.fetchSnapshot).toHaveBeenCalledTimes(2)
  })
})
