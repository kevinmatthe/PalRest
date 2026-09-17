import { useEffect, useState } from 'react'
import type { DesktopBridge } from './bridge'

/** Native hide does not reliably change WebView document.visibilityState. */
export function useWindowVisibility(bridge?: DesktopBridge) {
  const [documentVisible, setDocumentVisible] = useState(() => document.visibilityState !== 'hidden')
  const [nativeVisible, setNativeVisible] = useState(() => !bridge?.isOverlayVisible)
  useEffect(() => {
    const changed = () => setDocumentVisible(document.visibilityState !== 'hidden')
    document.addEventListener('visibilitychange', changed)
    return () => document.removeEventListener('visibilitychange', changed)
  }, [])
  useEffect(() => {
    if (!bridge?.isOverlayVisible) { setNativeVisible(true); return }
    let active = true
    let revision = 0
    let cleanup: (() => void) | undefined
    const attach = async () => {
      try {
        const unlisten = await bridge.onOverlayVisibilityChanged?.(visible => {
          revision++; if (active) setNativeVisible(visible)
        })
        if (!active) { unlisten?.(); return }
        cleanup = unlisten
        const before = revision
        const visible = await bridge.isOverlayVisible!()
        if (active && before === revision) setNativeVisible(visible)
      } catch { if (active && revision === 0) setNativeVisible(true) }
    }
    void attach()
    return () => { active = false; cleanup?.() }
  }, [bridge])
  return documentVisible && nativeVisible
}
