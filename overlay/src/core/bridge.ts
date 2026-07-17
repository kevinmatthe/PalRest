import type { OverlayConfigV1 } from './config'
import { invoke, isTauri } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'

export type FetchPresentationRequest = {
  baseUrl: string
  gameId: string
  userId: string
  etag?: string
}

export type FetchPresentationResult =
  | { status: 200; etag?: string; body: unknown }
  | { status: 304 }
  | { status: 404; code: 'player_not_found' | 'game_not_supported' | 'presentation_unsupported' }
  | { status: 503; code: 'presentation_unavailable' }

export interface OverlayBridge {
  fetchPresentation(
    request: FetchPresentationRequest,
    signal: AbortSignal,
  ): Promise<FetchPresentationResult>
}

export type PlayerListItem = {
  user_id: string
  name: string
  account_name: string
}

export class ConfigSaveError extends Error {
  readonly persisted: boolean

  constructor(persisted: boolean) {
    super(persisted ? 'configuration synchronization failed' : 'configuration save failed')
    this.name = 'ConfigSaveError'
    this.persisted = persisted
  }
}

export function configSaveWasPersisted(error: unknown): boolean {
  return typeof error === 'object' && error !== null &&
    'persisted' in error && error.persisted === true
}

export interface DesktopBridge extends OverlayBridge {
  loadConfig(): Promise<OverlayConfigV1 | null>
  saveConfig(config: OverlayConfigV1): Promise<void>
  listPlayers(baseUrl: string, signal: AbortSignal): Promise<PlayerListItem[]>
  currentWindowLabel(): Promise<'overlay' | 'settings'>
  setAdjustmentMode(enabled: boolean): Promise<void>
  openSettings?(): Promise<void>
  currentPlatform?(): Promise<'windows' | 'macos' | string>
  detectedPalworldUserId?(): Promise<string | null>
  onAdjustmentModeChanged?(handler: (enabled: boolean) => void): Promise<() => void>
  onReselectPlayer?(handler: () => void): Promise<() => void>
  onConfigChanged?(handler: (config: unknown) => void): Promise<() => void>
}

export function createBrowserPlaceholderBridge(): PresentationDesktopBridge {
  return {
    async currentWindowLabel() { return 'overlay' },
    async loadConfig() { return null },
    async saveConfig() { throw new Error('desktop bridge unavailable') },
    async listPlayers() { throw new Error('desktop bridge unavailable') },
    async setAdjustmentMode() { throw new Error('desktop bridge unavailable') },
    async fetchPresentation() { throw new Error('desktop bridge unavailable') },
    async onAdjustmentModeChanged() { return () => {} },
    async onReselectPlayer() { return () => {} },
    async onConfigChanged() { return () => {} },
  }
}

function abortError(): DOMException {
  return new DOMException('The operation was aborted', 'AbortError')
}

function createHttpInvokeGate() {
  let tail: Promise<void> = Promise.resolve()
  return <T>(command: string, args: Record<string, unknown>, signal: AbortSignal): Promise<T> => {
    if (signal.aborted) return Promise.reject(abortError())
    let resolveUser!: (value: T) => void
    let rejectUser!: (reason?: unknown) => void
    const userResult = new Promise<T>((resolve, reject) => {
      resolveUser = resolve
      rejectUser = reject
    })
    const onAbort = () => rejectUser(abortError())
    signal.addEventListener('abort', onAbort, { once: true })

    const execute = async () => {
      if (signal.aborted) {
        signal.removeEventListener('abort', onAbort)
        rejectUser(abortError())
        return
      }
      let nativeResult: Promise<T>
      try {
        nativeResult = Promise.resolve(invoke<T>(command, args))
      } catch (error: unknown) {
        signal.removeEventListener('abort', onAbort)
        rejectUser(error)
        return
      }
      nativeResult.then(
        (value) => { if (!signal.aborted) resolveUser(value) },
        (error: unknown) => { if (!signal.aborted) rejectUser(error) },
      )
      await nativeResult.catch(() => undefined)
      signal.removeEventListener('abort', onAbort)
    }
    tail = tail.then(execute, execute)
    return userResult
  }
}

type PresentationDesktopBridge = DesktopBridge & Required<Pick<OverlayBridge, 'fetchPresentation'>>

function createTauriBridge(): PresentationDesktopBridge {
  const invokeHttp = createHttpInvokeGate()
  return {
    currentWindowLabel: () => invoke('current_window_label'),
    loadConfig: () => invoke('load_config'),
    saveConfig: async (config) => {
      try {
        await invoke('save_config', { config })
      } catch (error) {
        throw new ConfigSaveError(configSaveWasPersisted(error))
      }
    },
    listPlayers: (baseUrl, signal) => invokeHttp('list_players', { baseUrl }, signal),
    setAdjustmentMode: (enabled) => invoke('set_adjustment_mode', { enabled }),
    fetchPresentation: (request, signal) => invokeHttp('fetch_presentation', { request }, signal),
    currentPlatform: () => invoke('current_platform'),
    detectedPalworldUserId: () => invoke('detected_palworld_user_id'),
    onAdjustmentModeChanged: (handler) => listen<unknown>('adjustment-mode-changed', (event) => {
      if (typeof event.payload === 'boolean') handler(event.payload)
    }),
    onReselectPlayer: (handler) => listen('reselect-player', handler),
    onConfigChanged: (handler) => listen<unknown>('overlay-config-changed', (event) => handler(event.payload)),
  }
}

export function createDesktopBridge(): PresentationDesktopBridge {
  return isTauri() ? createTauriBridge() : createBrowserPlaceholderBridge()
}
