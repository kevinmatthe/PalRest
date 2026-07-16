import { parseSnapshot, type Snapshot } from '../contracts/snapshot'
import type { FetchSnapshotRequest, FetchSnapshotResult, OverlayBridge } from './bridge'

export type SnapshotPollerState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; snapshot: Snapshot }
  | { status: 'stale'; snapshot: Snapshot }
  | { status: 'disconnected'; snapshot?: Snapshot }
  | { status: 'needs-player'; code: 'player_not_found' }
  | { status: 'incompatible'; reason: string }

export interface SnapshotPollerConfig {
  baseUrl: string
  gameId: string
  userId: string
}

export interface SnapshotPollerOptions {
  bridge: OverlayBridge
  config: SnapshotPollerConfig
  now?: () => number
}

type Listener = () => void
type Timer = ReturnType<typeof setTimeout>

const POLL_DELAY_MS = 2_000
const REQUEST_TIMEOUT_MS = 5_000
const MAX_RETRY_DELAY_MS = 30_000

export class SnapshotPoller {
  private state: SnapshotPollerState = { status: 'idle' }
  private readonly listeners = new Set<Listener>()
  private readonly bridge: OverlayBridge
  private readonly config: SnapshotPollerConfig
  private readonly now: () => number
  private running = false
  private generation = 0
  private failureCount = 0
  private etag: string | undefined
  private lastSnapshot: Snapshot | undefined
  private pollTimer: Timer | undefined
  private freshnessTimer: Timer | undefined
  private requestTimer: Timer | undefined
  private controller: AbortController | undefined

  constructor({ bridge, config, now = () => Date.now() }: SnapshotPollerOptions) {
    this.bridge = bridge
    this.config = { ...config }
    this.now = now
  }

  getState(): SnapshotPollerState {
    return this.state
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  start(): void {
    if (this.running) return
    this.running = true
    this.generation += 1
    this.failureCount = 0
    this.publish({ status: 'loading' })
    void this.request(this.generation)
  }

  stop(): void {
    this.running = false
    this.generation += 1
    this.clearAllTimers()
    this.controller?.abort()
    this.controller = undefined
    this.publish({ status: 'idle' })
  }

  private publish(state: SnapshotPollerState): void {
    this.state = state
    for (const listener of this.listeners) {
      try {
        listener()
      } catch {
        // A consumer cannot interrupt polling or other consumers.
      }
    }
  }

  private async request(generation: number): Promise<void> {
    if (!this.isCurrent(generation) || this.controller) return

    const controller = new AbortController()
    this.controller = controller
    let timedOut = false
    this.requestTimer = setTimeout(() => {
      timedOut = true
      this.requestTimer = undefined
      if (!this.isCurrent(generation) || this.controller !== controller) return
      this.controller = undefined
      controller.abort()
      this.handleTransientFailure(generation)
    }, REQUEST_TIMEOUT_MS)

    const request: FetchSnapshotRequest = { ...this.config }
    if (this.etag !== undefined) request.etag = this.etag

    try {
      const result = await this.bridge.fetchSnapshot(request, controller.signal)
      if (!this.isCurrent(generation) || timedOut) return
      this.finishRequest()
      this.handleResult(result, generation)
    } catch {
      if (!this.isCurrent(generation) || timedOut) return
      this.finishRequest()
      this.handleTransientFailure(generation)
    }
  }

  private handleResult(result: FetchSnapshotResult, generation: number): void {
    if (result.status === 200) {
      let parsed: Snapshot
      try {
        parsed = parseSnapshot(result.body)
      } catch (error) {
        this.enterTerminal({
          status: 'incompatible',
          reason: error instanceof Error ? error.message : 'unsupported snapshot',
        })
        return
      }
      this.etag = result.etag
      this.lastSnapshot = parsed
      this.failureCount = 0
      this.publishFreshness(parsed)
      this.schedulePoll(POLL_DELAY_MS, generation)
      return
    }

    if (result.status === 304) {
      if (!this.lastSnapshot) {
        this.handleTransientFailure(generation)
        return
      }
      this.failureCount = 0
      this.publishFreshness(this.lastSnapshot)
      this.schedulePoll(POLL_DELAY_MS, generation)
      return
    }

    if (result.status === 404 && result.code === 'player_not_found') {
      this.enterTerminal({ status: 'needs-player', code: 'player_not_found' })
      return
    }

    if (result.status === 404) {
      this.enterTerminal({ status: 'incompatible', reason: 'game_not_supported' })
      return
    }

    this.handleTransientFailure(generation)
  }

  private handleTransientFailure(generation: number): void {
    this.publish(this.lastSnapshot
      ? { status: 'disconnected', snapshot: this.lastSnapshot }
      : { status: 'disconnected' })
    const delay = Math.min(POLL_DELAY_MS * (2 ** this.failureCount), MAX_RETRY_DELAY_MS)
    this.failureCount += 1
    this.schedulePoll(delay, generation)
  }

  private publishFreshness(snapshot: Snapshot): void {
    this.clearFreshnessTimer()
    const expiresAt = Date.parse(snapshot.fresh_until)
    const remaining = expiresAt - this.now()
    if (remaining < 0) {
      this.publish({ status: 'stale', snapshot })
      return
    }

    this.publish({ status: 'ready', snapshot })
    this.freshnessTimer = setTimeout(() => {
      this.freshnessTimer = undefined
      if (this.running && this.lastSnapshot === snapshot) {
        this.publish({ status: 'stale', snapshot })
      }
    }, remaining + 1)
  }

  private schedulePoll(delay: number, generation: number): void {
    if (!this.isCurrent(generation)) return
    if (this.pollTimer !== undefined) clearTimeout(this.pollTimer)
    this.pollTimer = setTimeout(() => {
      this.pollTimer = undefined
      void this.request(generation)
    }, delay)
  }

  private enterTerminal(state: SnapshotPollerState): void {
    this.running = false
    this.clearAllTimers()
    this.controller?.abort()
    this.controller = undefined
    this.publish(state)
  }

  private finishRequest(): void {
    if (this.requestTimer !== undefined) clearTimeout(this.requestTimer)
    this.requestTimer = undefined
    this.controller = undefined
  }

  private clearFreshnessTimer(): void {
    if (this.freshnessTimer !== undefined) clearTimeout(this.freshnessTimer)
    this.freshnessTimer = undefined
  }

  private clearAllTimers(): void {
    if (this.pollTimer !== undefined) clearTimeout(this.pollTimer)
    if (this.requestTimer !== undefined) clearTimeout(this.requestTimer)
    this.pollTimer = undefined
    this.requestTimer = undefined
    this.clearFreshnessTimer()
  }

  private isCurrent(generation: number): boolean {
    return this.running && this.generation === generation
  }
}
