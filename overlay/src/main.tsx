import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import App from './App'
import type { DesktopBridge } from './core/bridge'

export function createBrowserPlaceholderBridge(): DesktopBridge {
  return {
    async currentWindowLabel() { return 'overlay' },
    async loadConfig() { return null },
    async saveConfig() { throw new Error('desktop bridge unavailable') },
    async listPlayers() { throw new Error('desktop bridge unavailable') },
    async setAdjustmentMode() { throw new Error('desktop bridge unavailable') },
    async fetchSnapshot() { throw new Error('desktop bridge unavailable') },
  }
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App bridge={createBrowserPlaceholderBridge()} />
  </StrictMode>,
)
