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
