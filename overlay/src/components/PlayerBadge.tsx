import type { Presentation, SourceStatus } from '../contracts/presentation'

const SOURCE_STATUS_COPY: Record<SourceStatus, string> = {
  online: '在线',
  offline: '离线',
  unknown: '状态未知',
}

export interface PlayerBadgeProps {
  presentation: Presentation
}

export function PlayerBadge({ presentation }: PlayerBadgeProps) {
  const name = presentation.identity.display_name.trim()
  const initial = Array.from(name)[0] ?? '玩'

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
