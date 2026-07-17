import type { LayoutProfile } from '../core/layout'

export interface GameAdapter {
  id: string
  title: string
  processHints: {
    windows: string[]
    macos: string[]
  }
  readonly defaultLayout: LayoutProfile
}
