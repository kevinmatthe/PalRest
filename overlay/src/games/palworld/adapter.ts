import type { TimerTone } from '../../contracts/snapshot'
import type { GameAdapter } from '../types'

const TONE_STRENGTH: Record<TimerTone, number> = {
  muted: 0,
  normal: 1,
  warning: 2,
  danger: 3,
}

const TONES = ['muted', 'normal', 'warning', 'danger'] as const
const MINUTE_MS = 60_000
const HOUR_MINUTES = 60
const DAY_MINUTES = 24 * HOUR_MINUTES

export const palworldAdapter: GameAdapter = {
  id: 'palworld',
  title: '幻兽帕鲁',
  processHints: {
    windows: ['Palworld-Win64-Shipping.exe'],
    macos: ['Palworld.app'],
  },
  formatDuration(ms) {
    if (!Number.isFinite(ms)) return '0分钟'

    const sign = ms < 0 ? '-' : ''
    const totalMinutes = Math.round(Math.abs(ms) / MINUTE_MS)
    if (totalMinutes === 0) return '0分钟'

    const days = Math.floor(totalMinutes / DAY_MINUTES)
    const hours = Math.floor((totalMinutes % DAY_MINUTES) / HOUR_MINUTES)
    const minutes = totalMinutes % HOUR_MINUTES
    const parts: string[] = []
    if (days > 0) parts.push(`${days}天`)
    if (hours > 0) parts.push(`${hours}小时`)
    if (minutes > 0) parts.push(`${minutes}分钟`)
    return `${sign}${parts.join('')}`
  },
  overallTone(snapshot) {
    if (!snapshot.timers?.length) return 'muted'

    let strength = 0
    for (const timer of snapshot.timers) {
      strength = Math.max(strength, TONE_STRENGTH[timer.tone])
    }
    return TONES[strength]
  },
}
