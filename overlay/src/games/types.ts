import type { Snapshot } from '../contracts/snapshot'

export interface GameAdapter {
  id: string
  title: string
  processHints: {
    windows: string[]
    macos: string[]
  }
  formatDuration(ms: number): string
  overallTone(snapshot: Snapshot): 'normal' | 'warning' | 'danger' | 'muted'
}
