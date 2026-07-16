import type { OverlayConfigV1 } from './config'
import { invoke, isTauri } from '@tauri-apps/api/core'

export type FetchSnapshotRequest = {
  baseUrl: string
  gameId: string
  userId: string
  etag?: string
}

export type FetchSnapshotResult =
  | { status: 200; etag?: string; body: unknown }
  | { status: 304 }
  | { status: 404; code: 'player_not_found' | 'game_not_supported' }
  | { status: 503; code: 'snapshot_unavailable' }

export interface OverlayBridge {
  fetchSnapshot(
    request: FetchSnapshotRequest,
    signal: AbortSignal,
  ): Promise<FetchSnapshotResult>
}

export type PlayerListItem = {
  user_id: string
  name: string
  account_name: string
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
}

export function createBrowserPlaceholderBridge(): DesktopBridge {
  return {
    async currentWindowLabel() { return 'overlay' },
    async loadConfig() { return null },
    async saveConfig() { throw new Error('desktop bridge unavailable') },
    async listPlayers() { throw new Error('desktop bridge unavailable') },
    async setAdjustmentMode() { throw new Error('desktop bridge unavailable') },
    async fetchSnapshot() { throw new Error('desktop bridge unavailable') },
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

function createTauriBridge(): DesktopBridge {
  const invokeHttp = createHttpInvokeGate()
  return {
    currentWindowLabel: () => invoke('current_window_label'),
    loadConfig: () => invoke('load_config'),
    saveConfig: (config) => invoke('save_config', { config }),
    listPlayers: (baseUrl, signal) => invokeHttp('list_players', { baseUrl }, signal),
    setAdjustmentMode: (enabled) => invoke('set_adjustment_mode', { enabled }),
    fetchSnapshot: (request, signal) => invokeHttp('fetch_snapshot', { request }, signal),
    currentPlatform: () => invoke('current_platform'),
    detectedPalworldUserId: () => invoke('detected_palworld_user_id'),
  }
}

export function createDesktopBridge(): DesktopBridge {
  return isTauri() ? createTauriBridge() : createBrowserPlaceholderBridge()
}
