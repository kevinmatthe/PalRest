import type { Snapshot } from '../contracts/snapshot'
import type { LayoutProfile } from '../core/layout'

export interface GameAdapter {
  id: string
  title: string
  processHints: {
    windows: string[]
    macos: string[]
  }
  readonly defaultLayout: LayoutProfile
  formatDuration(ms: number): string
  overallTone(snapshot: Snapshot): 'normal' | 'warning' | 'danger' | 'muted'
}
