import { cloneLayoutProfile, PALWORLD_DEFAULT_LAYOUT } from '../../core/layout'
import type { GameAdapter } from '../types'

export const palworldAdapter: GameAdapter = {
  id: 'palworld',
  title: '幻兽帕鲁',
  processHints: {
    windows: ['Palworld-Win64-Shipping.exe'],
    macos: ['Palworld.app'],
  },
  get defaultLayout() {
    return cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
  },
}
