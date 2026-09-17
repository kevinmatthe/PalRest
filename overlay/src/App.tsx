import { lazy, Suspense, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'

import { OverlayBar } from './components/OverlayBar'
import type { DesktopBridge } from './core/bridge'
import { parseOverlayConfig, type OverlayConfigV1 } from './core/config'
import { PresentationPoller } from './core/presentationPoller'
import { palworldAdapter } from './games/palworld/adapter'
import { SettingsView } from './settings/SettingsView'
import { useWindowVisibility } from './core/useWindowVisibility'
import './styles.css'

const TeamMap = lazy(() => import('./components/TeamMap'))

export interface AppProps { bridge: DesktopBridge }

type Bootstrap =
  | { status: 'loading' }
  | { status: 'error' }
  | {
      status: 'ready'
      label: 'overlay' | 'settings' | 'team-map'
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
  const poller = useMemo(() => new PresentationPoller({
    bridge,
    config: { baseUrl: config.baseUrl, gameId: config.gameId, userId: config.userId },
  }), [bridge])
  const state = useSyncExternalStore(poller.subscribe, poller.getState, poller.getState)
  const visible = useWindowVisibility(bridge)

  useEffect(() => {
    poller.updateConfig({ baseUrl: config.baseUrl, gameId: config.gameId, userId: config.userId })
  }, [config.baseUrl, config.gameId, config.userId, poller])

  useEffect(() => {
    if (visible) poller.start()
    else poller.stop()
    return () => poller.stop()
  }, [poller, visible])

  useEffect(() => {
    if (state.status === 'needs-player' && bridge.openSettings) {
      void bridge.openSettings().catch(() => undefined)
    }
  }, [bridge, state.status])

  const layout = 'layouts' in config
    ? (config.layouts[config.gameId] ?? palworldAdapter.defaultLayout)
    : palworldAdapter.defaultLayout

  if (!visible) return null
  if (state.status === 'ready' || state.status === 'stale') {
    return <OverlayBar presentation={state.presentation} layout={layout} status={state.status} mapBaseUrl={config.baseUrl} scale={config.scale} adjustMode={adjustMode} />
  }
  if (state.status === 'disconnected' && state.presentation) {
    return <OverlayBar presentation={state.presentation} layout={layout} status="disconnected" mapBaseUrl={config.baseUrl} scale={config.scale} adjustMode={adjustMode} />
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
  const latestConfig = useRef<OverlayConfigV1 | null>(null)
  const configListenerReady = useRef<Promise<void> | null>(null)

  useEffect(() => {
    let active = true
    let markConfigListenerReady = () => {}
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
      latestConfig.current = null
      configListenerReady.current = new Promise((resolve) => { markConfigListenerReady = resolve })
      const registration = bridge.onConfigChanged((rawConfig) => {
        if (!active) return
        const config = parseOverlayConfig(rawConfig)
        if (!config) return
        latestConfig.current = config
        setBootstrap((current) => current.status === 'ready' && current.label !== 'settings'
          ? { ...current, config }
          : current)
      })
      attach(registration)
      void registration.then(markConfigListenerReady, markConfigListenerReady)
    } else {
      configListenerReady.current = null
    }
    return () => {
      active = false
      markConfigListenerReady()
      cleanups.splice(0).forEach((unlisten) => unlisten())
    }
  }, [bridge])

  useEffect(() => {
    let active = true
    const initialise = async () => {
      try {
        const registration = configListenerReady.current
        if (registration) await registration
        if (!active) return
        const [label, rawConfig] = await Promise.all([
          bridge.currentWindowLabel(),
          bridge.loadConfig(),
        ])
        if (!active) return
        if (label !== 'overlay' && label !== 'settings' && label !== 'team-map') {
          setBootstrap({ status: 'error' })
          return
        }
        const loadedConfig = rawConfig === null ? null : parseOverlayConfig(rawConfig)
        const config = label !== 'settings' && latestConfig.current
          ? latestConfig.current
          : loadedConfig
        if (rawConfig !== null && !loadedConfig && !config) {
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
      } catch {
        if (active) setBootstrap({ status: 'error' })
      }
    }
    void initialise()
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
  if (bootstrap.label === 'team-map' && bootstrap.config) return <Suspense fallback={<CompactState>正在打开全员地图…</CompactState>}><TeamMap bridge={bridge} config={bootstrap.config} /></Suspense>
  if (!bootstrap.config) return <CompactState adjustMode={adjustMode}>需要先完成设置</CompactState>
  return <LiveOverlay bridge={bridge} config={bootstrap.config} adjustMode={adjustMode} />
}
