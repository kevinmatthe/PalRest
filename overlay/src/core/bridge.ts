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

async function invokeAbortable<T>(
  command: string,
  args: Record<string, unknown>,
  signal: AbortSignal,
): Promise<T> {
  if (signal.aborted) throw abortError()
  return new Promise<T>((resolve, reject) => {
    const onAbort = () => reject(abortError())
    signal.addEventListener('abort', onAbort, { once: true })
    void invoke<T>(command, args).then(
      (value) => {
        signal.removeEventListener('abort', onAbort)
        if (signal.aborted) reject(abortError())
        else resolve(value)
      },
      (error: unknown) => {
        signal.removeEventListener('abort', onAbort)
        reject(error)
      },
    )
  })
}

function createTauriBridge(): DesktopBridge {
  return {
    currentWindowLabel: () => invoke('current_window_label'),
    loadConfig: () => invoke('load_config'),
    saveConfig: (config) => invoke('save_config', { config }),
    listPlayers: (baseUrl, signal) => invokeAbortable('list_players', { baseUrl }, signal),
    setAdjustmentMode: (enabled) => invoke('set_adjustment_mode', { enabled }),
    fetchSnapshot: (request, signal) => invokeAbortable('fetch_snapshot', { request }, signal),
    currentPlatform: () => invoke('current_platform'),
    detectedPalworldUserId: () => invoke('detected_palworld_user_id'),
  }
}

export function createDesktopBridge(): DesktopBridge {
  return isTauri() ? createTauriBridge() : createBrowserPlaceholderBridge()
}
