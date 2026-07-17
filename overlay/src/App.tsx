import { useEffect, useMemo, useState, useSyncExternalStore } from 'react'

import { OverlayBar } from './components/OverlayBar'
import type { DesktopBridge } from './core/bridge'
import { parseOverlayConfig, type OverlayConfigV1 } from './core/config'
import { SnapshotPoller } from './core/poller'
import { SettingsView } from './settings/SettingsView'
import './styles.css'

export interface AppProps { bridge: DesktopBridge }

type Bootstrap =
  | { status: 'loading' }
  | { status: 'error' }
  | {
      status: 'ready'
      label: 'overlay' | 'settings'
      config: OverlayConfigV1 | null
      platform?: string
      detectedUserId?: string | null
    }

function CompactState({ children, adjustMode = false }: { children: string; adjustMode?: boolean }) {
  const dragProps = adjustMode ? { 'data-tauri-drag-region': 'deep' } : {}
  return <main className={`overlay-state${adjustMode ? ' overlay-state--adjusting' : ''}`} role="status" {...dragProps}>
    <span>{children}</span>
    {adjustMode ? <span className="overlay__drag-hint">拖动调整位置</span> : null}
  </main>
}

function LiveOverlay({ bridge, config, adjustMode }: { bridge: DesktopBridge; config: OverlayConfigV1; adjustMode: boolean }) {
  const poller = useMemo(() => new SnapshotPoller({
    bridge,
    config: { baseUrl: config.baseUrl, gameId: config.gameId, userId: config.userId },
  }), [bridge, config])
  const state = useSyncExternalStore(poller.subscribe, poller.getState, poller.getState)

  useEffect(() => {
    poller.start()
    return () => poller.stop()
  }, [poller])

  useEffect(() => {
    if (state.status === 'needs-player' && bridge.openSettings) {
      void bridge.openSettings().catch(() => undefined)
    }
  }, [bridge, state.status])

  if (state.status === 'ready' || state.status === 'stale') {
    return <OverlayBar snapshot={state.snapshot} status={state.status} mapBaseUrl={config.baseUrl} scale={config.scale} adjustMode={adjustMode} />
  }
  if (state.status === 'disconnected' && state.snapshot) {
    return <OverlayBar snapshot={state.snapshot} status="disconnected" mapBaseUrl={config.baseUrl} scale={config.scale} adjustMode={adjustMode} />
  }
  if (state.status === 'needs-player') return <CompactState adjustMode={adjustMode}>玩家已失效，请在设置中重新选择</CompactState>
  if (state.status === 'incompatible') return <CompactState adjustMode={adjustMode}>服务版本不兼容，请更新应用</CompactState>
  if (state.status === 'disconnected') return <CompactState adjustMode={adjustMode}>暂时无法连接服务</CompactState>
  return <CompactState adjustMode={adjustMode}>正在读取玩家状态…</CompactState>
}

export default function App({ bridge }: AppProps) {
  const [bootstrap, setBootstrap] = useState<Bootstrap>({ status: 'loading' })
  const [adjustMode, setAdjustMode] = useState(false)
  const [reselectSignal, setReselectSignal] = useState(0)

  useEffect(() => {
    let active = true
    const cleanups: Array<() => void> = []
    const attach = (subscription: Promise<() => void>) => {
      void subscription.then((unlisten) => {
        if (active) cleanups.push(unlisten)
        else unlisten()
      }).catch(() => undefined)
    }
    if (bridge.onAdjustmentModeChanged) {
      attach(bridge.onAdjustmentModeChanged((enabled) => { if (active) setAdjustMode(enabled) }))
    }
    if (bridge.onReselectPlayer) {
      attach(bridge.onReselectPlayer(() => { if (active) setReselectSignal((value) => value + 1) }))
    }
    if (bridge.onConfigChanged) {
      attach(bridge.onConfigChanged((rawConfig) => {
        if (!active) return
        const config = parseOverlayConfig(rawConfig)
        if (!config) return
        setBootstrap((current) => current.status === 'ready' && current.label === 'overlay'
          ? { ...current, config }
          : current)
      }))
    }
    return () => {
      active = false
      cleanups.splice(0).forEach((unlisten) => unlisten())
    }
  }, [bridge])

  useEffect(() => {
    let active = true
    const labelPromise = bridge.currentWindowLabel()
    const configPromise = bridge.loadConfig()
    void Promise.all([labelPromise, configPromise]).then(async ([label, rawConfig]) => {
      if (!active) return
      if (label !== 'overlay' && label !== 'settings') {
        setBootstrap({ status: 'error' })
        return
      }
      const config = rawConfig === null ? null : parseOverlayConfig(rawConfig)
      if (rawConfig !== null && !config) {
        setBootstrap({ status: 'error' })
        return
      }
      setAdjustMode(config ? !config.locked : false)
      let platform: string | undefined
      let detectedUserId: string | null | undefined
      const currentPlatform = bridge.currentPlatform
      const detectedPalworldUserId = bridge.detectedPalworldUserId
      if (label === 'settings' && currentPlatform) {
        try {
          platform = await currentPlatform()
          if (!active) return
          if (platform === 'windows' && detectedPalworldUserId) {
            try {
              detectedUserId = await detectedPalworldUserId()
            } catch {
              detectedUserId = null
            }
          }
        } catch {
          platform = undefined
        }
      }
      if (active) setBootstrap({ status: 'ready', label, config, platform, detectedUserId })
    }).catch(() => {
      if (active) setBootstrap({ status: 'error' })
    })
    return () => { active = false }
  }, [bridge])

  useEffect(() => {
    if (bootstrap.status !== 'ready' || bootstrap.label !== 'overlay' || bootstrap.config || !bridge.openSettings) return
    void bridge.openSettings().catch(() => undefined)
  }, [bootstrap, bridge])

  if (bootstrap.status === 'loading') return <CompactState adjustMode={adjustMode}>正在读取本地设置…</CompactState>
  if (bootstrap.status === 'error') return <CompactState adjustMode={adjustMode}>无法读取悬浮条设置</CompactState>
  if (bootstrap.label === 'settings') {
    return <SettingsView
      bridge={bridge}
      initialConfig={bootstrap.config}
      platform={bootstrap.platform}
      detectedUserId={bootstrap.detectedUserId}
      reselectSignal={reselectSignal}
      onSaved={(config) => setBootstrap({ ...bootstrap, config })}
    />
  }
  if (!bootstrap.config) return <CompactState adjustMode={adjustMode}>需要先完成设置</CompactState>
  return <LiveOverlay bridge={bridge} config={bootstrap.config} adjustMode={adjustMode} />
}
