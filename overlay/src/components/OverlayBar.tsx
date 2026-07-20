/// <reference types="vite/client" />

import { useCallback, useState, type CSSProperties } from 'react'

import type { DisplayTone, Presentation, SourceStatus } from '../contracts/presentation'
import { resolveProgress, resolveSlots, type LayoutProfile } from '../core/layout'
import {
  PALWORLD_PROJECTION_ID,
  PALWORLD_TILE_SET_ID,
  projectPalworldWorldToLeaflet,
  resolvePrivateTileUrl,
} from '../games/palworld/map'
import { PalworldMiniMap } from './PalworldMiniMap'
import { PlayerBadge } from './PlayerBadge'
import '../styles.css'

export type OverlayConnectionStatus = 'ready' | 'stale' | 'disconnected'

export interface OverlayBarProps {
  presentation: Presentation
  layout: LayoutProfile
  status: OverlayConnectionStatus
  adjustMode: boolean
  scale: number
  mapBaseUrl?: string
}

type OverlayStyle = CSSProperties & { '--overlay-scale': string }

const SOURCE_STATUS_COPY: Record<SourceStatus, string> = {
  online: '在线',
  offline: '离线',
  unknown: '状态未知',
}

const TONE_STRENGTH: Record<DisplayTone, number> = {
  muted: 0,
  normal: 1,
  warning: 2,
  danger: 3,
}

const STRENGTH_TONE = ['muted', 'normal', 'warning', 'danger'] as const

function formatObservedAt(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '时间未知'

  const year = date.getUTCFullYear()
  const month = String(date.getUTCMonth() + 1).padStart(2, '0')
  const day = String(date.getUTCDate()).padStart(2, '0')
  const hours = String(date.getUTCHours()).padStart(2, '0')
  const minutes = String(date.getUTCMinutes()).padStart(2, '0')
  return `${year}-${month}-${day} ${hours}:${minutes} UTC`
}

function dataStatusCopy(status: OverlayConnectionStatus, observedAt: string): string {
  if (status === 'ready') return `更新 ${observedAt}`
  if (status === 'stale') return `数据已过期 · 最后更新 ${observedAt}`
  return `连接已断开 · 最后更新 ${observedAt}`
}

function safeScale(scale: number): number {
  if (!Number.isFinite(scale)) return 1
  return Math.min(1.25, Math.max(0.8, scale))
}

function overallTone(presentation: Presentation): DisplayTone {
  let strength = 0
  for (const field of presentation.fields) {
    if (field.available) strength = Math.max(strength, TONE_STRENGTH[field.tone])
  }
  return STRENGTH_TONE[strength]
}

function usableMap(presentation: Presentation, mapBaseUrl: string | undefined): boolean {
  const map = presentation.map
  if (presentation.game_id !== 'palworld' || map === undefined || mapBaseUrl === undefined) return false
  if (map.projection !== PALWORLD_PROJECTION_ID || map.tile_set !== PALWORLD_TILE_SET_ID) return false
  if (resolvePrivateTileUrl(map.tile_url, mapBaseUrl) === null) return false
  try {
    projectPalworldWorldToLeaflet(map.x, map.y)
    return true
  } catch {
    return false
  }
}

export function OverlayBar({
  presentation,
  layout,
  status,
  adjustMode,
  scale,
  mapBaseUrl,
}: OverlayBarProps) {
  const [failedMapIdentity, setFailedMapIdentity] = useState<string | null>(null)
  const tone = overallTone(presentation)
  const fields = new Map(presentation.fields.map((field) => [field.id, field]))
  const slots = resolveSlots(fields, layout.slots)
  const progress = resolveProgress(presentation.fields, layout.progress)
  const mapUsable = usableMap(presentation, mapBaseUrl)
  const mapIdentity = mapUsable ? JSON.stringify([
    presentation.game_id,
    presentation.user_id,
    mapBaseUrl,
    presentation.map!.projection,
    presentation.map!.tile_set,
    presentation.map!.tile_url,
  ]) : null
  const showMap = layout.left.primary === 'map' && mapUsable && failedMapIdentity !== mapIdentity
  const handleMapUnavailable = useCallback(() => {
    if (mapIdentity !== null) setFailedMapIdentity(mapIdentity)
  }, [mapIdentity])
  const observedAt = formatObservedAt(presentation.observed_at)
  const rootClasses = [
    'overlay',
    'overlay--compact',
    `overlay--${tone}`,
    `overlay--${status}`,
    presentation.source_status === 'offline' ? 'overlay--offline' : '',
    adjustMode ? 'overlay--adjusting' : '',
  ].filter(Boolean).join(' ')
  const style: OverlayStyle = { '--overlay-scale': String(safeScale(scale)) }
  const dragProps = adjustMode ? { 'data-tauri-drag-region': 'deep' } : {}

  return (
    <section
      className={rootClasses}
      style={style}
      aria-label="幻兽帕鲁玩家状态悬浮条"
      {...dragProps}
    >
      <div className="overlay__frame">
        {showMap ? (
          <PalworldMiniMap
            key={mapIdentity}
            map={presentation.map!}
            serviceBaseUrl={mapBaseUrl!}
            onUnavailable={handleMapUnavailable}
          />
        ) : (
          <PlayerBadge presentation={presentation} />
        )}

        <div className="overlay__content">
          <header className="overlay__telemetry" data-testid="identity-header">
            <div className="overlay__identity">
              <span className="overlay__name">{presentation.identity.display_name}</span>
              {presentation.identity.level === undefined ? null : (
                <span className="overlay__level">Lv.{presentation.identity.level}</span>
              )}
              <span className="overlay__source-status">
                {SOURCE_STATUS_COPY[presentation.source_status]}
              </span>
            </div>
            <div className="overlay__meta" role="status" aria-label="数据状态">
              <span className="overlay__connection">{dataStatusCopy(status, observedAt)}</span>
            </div>
          </header>

          <dl className="overlay__fields" role="list" aria-label="玩家状态字段">
            {slots.map((slot, index) => (
              <div
                className={`overlay__field overlay__field--${slot.tone}`}
                role="listitem"
                aria-label={`${slot.label} ${slot.value}`}
                key={index}
              >
                <dt>{slot.label}</dt>
                <dd>{slot.value}</dd>
              </div>
            ))}
          </dl>
        </div>
      </div>

      {progress === undefined ? null : (
        <div
          className={`overlay__progress overlay__progress--${progress.tone}`}
          role="progressbar"
          aria-label={`${progress.field.label}进度`}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(progress.progress * 100)}
        >
          <span style={{ width: `${progress.progress * 100}%` }} />
        </div>
      )}

      {adjustMode ? (
        <div className="overlay__drag-hint">
          <span aria-hidden="true">⠿</span>
          拖动调整位置
        </div>
      ) : null}
    </section>
  )
}
