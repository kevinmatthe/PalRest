import { parsePresentation, type Presentation } from '../contracts/presentation'
import type { FetchPresentationRequest, FetchPresentationResult } from './bridge'

export type PresentationPollerState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; presentation: Presentation }
  | { status: 'stale'; presentation: Presentation }
  | { status: 'disconnected'; presentation?: Presentation }
  | { status: 'needs-player'; code: 'player_not_found' }
  | { status: 'incompatible'; reason: string }

export interface PresentationPollerConfig {
  baseUrl: string
  gameId: string
  userId: string
}

export interface PresentationPollerOptions {
  bridge: PresentationBridge
  config: PresentationPollerConfig
  now?: () => number
}

interface PresentationBridge {
  fetchPresentation(
    request: FetchPresentationRequest,
    signal: AbortSignal,
  ): Promise<FetchPresentationResult>
}

type Listener = () => void
type Timer = ReturnType<typeof setTimeout>

const POLL_DELAY_MS = 2_000
const REQUEST_TIMEOUT_MS = 5_000
const MAX_RETRY_DELAY_MS = 30_000
const MAX_TIMER_DELAY_MS = 2_147_483_647

export class PresentationPoller {
  private state: PresentationPollerState = { status: 'idle' }
  private readonly listeners = new Set<Listener>()
  private readonly bridge: PresentationBridge
  private config: PresentationPollerConfig
  private readonly now: () => number
  private running = false
  private explicitlyStopped = true
  private generation = 0
  private failureCount = 0
  private retryDueAt: number | undefined
  private etag: string | undefined
  private lastPresentation: Presentation | undefined
  private pollTimer: Timer | undefined
  private freshnessTimer: Timer | undefined
  private requestTimer: Timer | undefined
  private controller: AbortController | undefined
  private inFlight: symbol | undefined

  constructor({ bridge, config, now = () => Date.now() }: PresentationPollerOptions) {
    this.bridge = bridge
    this.config = { ...config }
    this.now = now
  }

  readonly getState = (): PresentationPollerState => this.state

  readonly subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  start(): void {
    if (this.running) return
    this.explicitlyStopped = false
    this.running = true
    this.generation += 1
    this.failureCount = 0
    this.retryDueAt = undefined
    this.publish({ status: 'loading' })
    void this.request(this.generation)
  }

  updateConfig(config: PresentationPollerConfig): void {
    if (
      this.config.baseUrl === config.baseUrl &&
      this.config.gameId === config.gameId &&
      this.config.userId === config.userId
    ) return

    this.config = { ...config }
    this.etag = undefined
    this.lastPresentation = undefined
    this.failureCount = 0
    this.retryDueAt = undefined
    if (this.explicitlyStopped) return

    this.running = true
    this.generation += 1
    this.clearAllTimers()
    this.controller?.abort()
    this.publish({ status: 'loading' })
    if (!this.inFlight) void this.request(this.generation)
  }

  stop(): void {
    if (
      !this.running && this.state.status === 'idle' &&
      this.controller === undefined && this.pollTimer === undefined &&
      this.freshnessTimer === undefined && this.requestTimer === undefined
    ) return
    this.explicitlyStopped = true
    this.running = false
    this.generation += 1
    this.retryDueAt = undefined
    this.clearAllTimers()
    this.controller?.abort()
    this.controller = undefined
    this.publish({ status: 'idle' })
  }

  private publish(state: PresentationPollerState): void {
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
    if (!this.isCurrent(generation) || this.inFlight) return

    const token = Symbol('presentation-request')
    const controller = new AbortController()
    this.inFlight = token
    this.controller = controller
    let timedOut = false
    this.requestTimer = setTimeout(() => {
      timedOut = true
      this.requestTimer = undefined
      if (
        !this.isCurrent(generation) || this.inFlight !== token ||
        this.controller !== controller
      ) return
      controller.abort()
      this.handleTransientFailure(generation)
    }, REQUEST_TIMEOUT_MS)

    const request: FetchPresentationRequest = { ...this.config }
    if (this.etag !== undefined) request.etag = this.etag

    try {
      const result = await this.bridge.fetchPresentation(request, controller.signal)
      if (!this.releaseRequest(token)) return
      if (!this.isCurrent(generation)) {
        this.startCurrentRequest()
        return
      }
      if (timedOut) {
        this.scheduleRetry(generation)
        return
      }
      this.handleResult(result, generation)
    } catch {
      if (!this.releaseRequest(token)) return
      if (!this.isCurrent(generation)) {
        this.startCurrentRequest()
        return
      }
      if (timedOut) {
        this.scheduleRetry(generation)
        return
      }
      this.handleTransientFailure(generation)
    }
  }

  private handleResult(result: FetchPresentationResult, generation: number): void {
    if (result.status === 200) {
      const schema = this.explicitSchema(result.body)
      if (schema !== undefined && schema !== 'overlay.presentation/v1') {
        this.enterTerminal({ status: 'incompatible', reason: 'unsupported presentation schema' })
        return
      }

      let parsed: Presentation
      try {
        parsed = parsePresentation(result.body)
      } catch {
        this.handleTransientFailure(generation)
        return
      }
      if (parsed.game_id !== this.config.gameId) {
        this.enterTerminal({
          status: 'incompatible',
          reason: 'game_id does not match request',
        })
        return
      }
      if (parsed.user_id !== this.config.userId) {
        this.enterTerminal({
          status: 'incompatible',
          reason: 'user_id does not match request',
        })
        return
      }
      this.etag = result.etag
      this.lastPresentation = parsed
      this.failureCount = 0
      this.retryDueAt = undefined
      this.publishFreshness(parsed)
      this.schedulePoll(POLL_DELAY_MS, generation)
      return
    }

    if (result.status === 304) {
      if (!this.lastPresentation) {
        this.handleTransientFailure(generation)
        return
      }
      this.failureCount = 0
      this.retryDueAt = undefined
      this.publishFreshness(this.lastPresentation)
      this.schedulePoll(POLL_DELAY_MS, generation)
      return
    }

    if (result.status === 404 && result.code === 'player_not_found') {
      this.enterTerminal({ status: 'needs-player', code: 'player_not_found' })
      return
    }

    if (result.status === 404) {
      this.enterTerminal({ status: 'incompatible', reason: result.code })
      return
    }

    this.handleTransientFailure(generation)
  }

  private handleTransientFailure(generation: number): void {
    if (!this.isCurrent(generation)) return
    const delay = Math.min(POLL_DELAY_MS * (2 ** this.failureCount), MAX_RETRY_DELAY_MS)
    if (delay < MAX_RETRY_DELAY_MS) this.failureCount += 1
    this.retryDueAt = this.now() + delay
    this.publish(this.lastPresentation
      ? { status: 'disconnected', presentation: this.lastPresentation }
      : { status: 'disconnected' })
    if (!this.isCurrent(generation) || this.controller) return
    this.scheduleRetry(generation)
  }

  private publishFreshness(presentation: Presentation): void {
    this.clearFreshnessTimer()
    const expiresAt = Date.parse(presentation.fresh_until)
    if (expiresAt < this.now()) {
      if (this.state.status !== 'stale' || this.state.presentation !== presentation) {
        this.publish({ status: 'stale', presentation })
      }
      return
    }
    if (this.state.status !== 'ready' || this.state.presentation !== presentation) {
      this.publish({ status: 'ready', presentation })
    }
    this.scheduleFreshnessDeadline(presentation, expiresAt)
  }

  private schedulePoll(delay: number, generation: number): void {
    if (!this.isCurrent(generation)) return
    if (this.pollTimer !== undefined) clearTimeout(this.pollTimer)
    this.pollTimer = setTimeout(() => {
      this.pollTimer = undefined
      void this.request(generation)
    }, delay)
  }

  private enterTerminal(state: PresentationPollerState): void {
    this.running = false
    this.retryDueAt = undefined
    this.clearAllTimers()
    this.controller?.abort()
    this.controller = undefined
    this.publish(state)
  }

  private releaseRequest(token: symbol): boolean {
    if (this.inFlight !== token) return false
    if (this.requestTimer !== undefined) clearTimeout(this.requestTimer)
    this.requestTimer = undefined
    this.controller = undefined
    this.inFlight = undefined
    return true
  }

  private startCurrentRequest(): void {
    if (this.running) void this.request(this.generation)
  }

  private scheduleRetry(generation: number): void {
    if (!this.isCurrent(generation) || this.retryDueAt === undefined || this.controller) return
    this.schedulePoll(Math.max(0, this.retryDueAt - this.now()), generation)
  }

  private scheduleFreshnessDeadline(presentation: Presentation, expiresAt: number): void {
    if (!this.running || this.lastPresentation !== presentation) return
    const remaining = expiresAt - this.now()
    if (remaining < 0) {
      if (this.state.status !== 'stale' || this.state.presentation !== presentation) {
        this.publish({ status: 'stale', presentation })
      }
      return
    }

    this.freshnessTimer = setTimeout(() => {
      this.freshnessTimer = undefined
      this.scheduleFreshnessDeadline(presentation, expiresAt)
    }, Math.min(remaining + 1, MAX_TIMER_DELAY_MS))
  }

  private explicitSchema(body: unknown): string | undefined {
    if (typeof body !== 'object' || body === null || Array.isArray(body)) return undefined
    const schema = (body as Record<string, unknown>).schema
    return typeof schema === 'string' ? schema : undefined
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
