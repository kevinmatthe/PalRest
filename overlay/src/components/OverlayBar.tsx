/// <reference types="vite/client" />

import type { CSSProperties } from 'react'

import type { Snapshot, SourceStatus, Timer } from '../contracts/snapshot'
import type { GameAdapter } from '../games/types'
import { palworldAdapter } from '../games/palworld/adapter'
import { PalworldMiniMap } from './PalworldMiniMap'
import '../styles.css'

export type OverlayConnectionStatus = 'ready' | 'stale' | 'disconnected'

export interface OverlayBarProps {
  snapshot: Snapshot
  status?: OverlayConnectionStatus
  adapter?: GameAdapter
  adjustMode?: boolean
  scale?: number
  mapBaseUrl?: string
}

type OverlayStyle = CSSProperties & { '--overlay-scale': string }

const SOURCE_STATUS_COPY: Record<SourceStatus, string> = {
  online: '在线',
  offline: '离线',
  unknown: '状态未知',
}

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

function identityCopy(snapshot: Snapshot): string {
  const level = snapshot.identity.level
  return level === undefined
    ? snapshot.identity.display_name
    : `${snapshot.identity.display_name} · Lv.${level}`
}

function progressTimer(timers: Timer[] | undefined): Timer | undefined {
  if (!timers) return undefined
  let fallback: Timer | undefined
  for (const timer of timers) {
    if (timer.progress === undefined) continue
    if (timer.id === 'policy_cycle_used') return timer
    fallback ??= timer
  }
  return fallback
}

function safeScale(scale: number): number {
  if (!Number.isFinite(scale)) return 1
  return Math.min(1.25, Math.max(0.8, scale))
}

export function OverlayBar({
  snapshot,
  status = 'ready',
  adapter = palworldAdapter,
  adjustMode = false,
  scale = 1,
  mapBaseUrl,
}: OverlayBarProps) {
  const tone = adapter.overallTone(snapshot)
  const observedAt = formatObservedAt(snapshot.observed_at)
  const sourceStatus = SOURCE_STATUS_COPY[snapshot.source_status]
  const hasLatency = snapshot.capabilities.includes('latency') && snapshot.latency !== undefined
  const hasTimers = snapshot.capabilities.includes('timers') && snapshot.timers !== undefined
  const hasMap = snapshot.capabilities.includes('map') && snapshot.map !== undefined
  const hasPalworldMap = snapshot.game_id === palworldAdapter.id &&
    adapter.id === palworldAdapter.id
  const railTimer = progressTimer(hasTimers ? snapshot.timers : undefined)
  const rootClasses = [
    'overlay',
    'overlay--compact',
    `overlay--${tone}`,
    `overlay--${status}`,
    snapshot.source_status === 'offline' ? 'overlay--offline' : '',
    hasMap ? '' : 'overlay--without-map',
    adjustMode ? 'overlay--adjusting' : '',
  ].filter(Boolean).join(' ')
  const style: OverlayStyle = { '--overlay-scale': String(safeScale(scale)) }
  const dragProps = adjustMode ? { 'data-tauri-drag-region': 'deep' } : {}

  return (
    <section
      className={rootClasses}
      style={style}
      aria-label={`${adapter.title}玩家状态悬浮条`}
      {...dragProps}
    >
      <div className="overlay__frame">
        {hasMap ? (
          hasPalworldMap && mapBaseUrl ? (
            <PalworldMiniMap map={snapshot.map!} serviceBaseUrl={mapBaseUrl} />
          ) : (
            <div
              className="overlay__locator"
              data-testid="capability-map"
              data-capability="map"
              style={{ pointerEvents: 'none' }}
            >
              <span role="status" className="overlay__locator-label">地图不可用</span>
            </div>
          )
        ) : null}

        <div className="overlay__content">
          <div className="overlay__telemetry">
            <div
              className="overlay__identity"
              data-testid="capability-identity"
              data-capability="identity"
            >
              <span className="overlay__name">{identityCopy(snapshot)}</span>
              <span className="overlay__source-status">{sourceStatus}</span>
            </div>

            <div className="overlay__meta" role="status" aria-label="数据状态">
              <span className="overlay__connection">
                {dataStatusCopy(status, observedAt)}
              </span>
              {hasLatency ? (
                <span
                  className="overlay__latency"
                  data-testid="capability-latency"
                  data-capability="latency"
                  aria-label={`延迟 ${Math.round(snapshot.latency!.milliseconds)} 毫秒`}
                >
                  {Math.round(snapshot.latency!.milliseconds)} ms
                </span>
              ) : null}
            </div>
          </div>

          {hasTimers ? (
            <dl
              className="overlay__timers"
              data-testid="capability-timers"
              data-capability="timers"
              aria-label="玩家计时"
            >
              {snapshot.timers!.map((timer) => (
                <div className={`overlay__timer overlay__timer--${timer.tone}`} key={timer.id}>
                  <dt>{timer.label}</dt>
                  <dd>{adapter.formatDuration(timer.value_ms)}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <div className="overlay__empty-rail" aria-hidden="true" />
          )}
        </div>
      </div>

      {railTimer?.progress !== undefined ? (
        <div
          className={`overlay__progress overlay__progress--${tone}`}
          role="progressbar"
          aria-label={`${railTimer.label}进度`}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(railTimer.progress * 100)}
        >
          <span style={{ width: `${Math.min(100, Math.max(0, railTimer.progress * 100))}%` }} />
        </div>
      ) : null}

      {adjustMode ? (
        <div className="overlay__drag-hint">
          <span aria-hidden="true">⠿</span>
          拖动调整位置
        </div>
      ) : null}
    </section>
  )
}
