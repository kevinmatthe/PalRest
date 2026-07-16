import type { OverlayConfigV1 } from './config'

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
