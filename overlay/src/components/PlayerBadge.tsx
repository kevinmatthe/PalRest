import type { Presentation, SourceStatus } from '../contracts/presentation'

const SOURCE_STATUS_COPY: Record<SourceStatus, string> = {
  online: '在线',
  offline: '离线',
  unknown: '状态未知',
}

export interface PlayerBadgeProps {
  presentation: Presentation
}

function codePointIn(value: string, start: number, end: number): boolean {
  const point = value.codePointAt(0)
  return point !== undefined && point >= start && point <= end
}

function fallbackGrapheme(value: string): string {
  const points = Array.from(value)
  if (points.length === 0) return '玩'

  let cluster = points[0]
  let index = 1
  if (codePointIn(cluster, 0x1f1e6, 0x1f1ff) &&
      points[1] !== undefined && codePointIn(points[1], 0x1f1e6, 0x1f1ff)) {
    return cluster + points[1]
  }

  while (index < points.length) {
    const point = points[index]
    const extension = /^\p{Mark}$/u.test(point) || point === '\ufe0e' || point === '\ufe0f' ||
      codePointIn(point, 0x1f3fb, 0x1f3ff)
    if (extension) {
      cluster += point
      index += 1
      continue
    }
    if (point === '\u200d' && points[index + 1] !== undefined) {
      cluster += point + points[index + 1]
      index += 2
      continue
    }
    break
  }
  return cluster
}

function firstGrapheme(value: string): string {
  const Segmenter = Intl.Segmenter
  if (typeof Segmenter === 'function') {
    const first = new Segmenter(undefined, { granularity: 'grapheme' })
      .segment(value)[Symbol.iterator]().next()
    if (!first.done) return first.value.segment
  }
  return fallbackGrapheme(value)
}

export function PlayerBadge({ presentation }: PlayerBadgeProps) {
  const name = presentation.identity.display_name.trim()
  const initial = firstGrapheme(name)

  return (
    <div className="player-badge" role="group" aria-label={`${name} 玩家徽章`}>
      <span className="player-badge__initial" aria-hidden="true">{initial}</span>
      <span className="player-badge__details">
        <span className="player-badge__name">{name}</span>
        <span className="player-badge__meta">
          {presentation.identity.level === undefined ? null : (
            <span>Lv.{presentation.identity.level}</span>
          )}
          <span>{SOURCE_STATUS_COPY[presentation.source_status]}</span>
        </span>
      </span>
    </div>
  )
}
