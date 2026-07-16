import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import App from './App'
import { createDesktopBridge } from './core/bridge'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App bridge={createDesktopBridge()} />
  </StrictMode>,
)
