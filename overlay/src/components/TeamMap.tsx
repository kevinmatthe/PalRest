import { useEffect, useRef, useState } from 'react'
import * as L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import type { DesktopBridge } from '../core/bridge'
import type { OverlayConfigV1 } from '../core/config'
import { PALWORLD_TILE_BOUNDS, resolvePrivateTileUrl } from '../games/palworld/map'
import { groupTeamPositions, type TeamPlayer } from '../map/teamPositions'
import { createTeleportLayer } from '../map/teamTeleportLayer'
import { useTeamPositions } from '../map/useTeamPositions'
import { useWindowVisibility } from '../core/useWindowVisibility'
import './teamMap.css'

const EMPTY: TeamPlayer[] = []
const textNode = (text: string) => { const node = document.createElement('span'); node.textContent = text; return node }
type View = 'all' | 'self' | 'manual'

export function TeamMap({ bridge, config }: { bridge: DesktopBridge; config: OverlayConfigV1 }) {
  const visible = useWindowVisibility()
  const positions = useTeamPositions(bridge.fetchLivePositions, config.baseUrl, visible)
  const players = positions.data?.players ?? EMPTY
  const [view, setView] = useState<View>('all')
  const [focus, setFocus] = useState(0)
  const [tileError, setTileError] = useState(false)
  const [closeError, setCloseError] = useState(false)
  const container = useRef<HTMLDivElement>(null)
  const mapRef = useRef<L.Map | null>(null)
  const teleportLayer = useRef<ReturnType<typeof createTeleportLayer> | null>(null)
  const markers = useRef(new Map<string, L.CircleMarker>())
  const automaticMove = useRef(false)
  const redraw = useRef<() => void>(() => {})
  const latest = useRef({ players, userID: config.userId, stale: positions.stale })
  latest.current = { players, userID: config.userId, stale: positions.stale }
  const self = players.find(p => p.user_id === config.userId)
  const updateCamera = useRef<() => void>(() => {})
  updateCamera.current = () => {
    const map = mapRef.current
    if (!map) return
    automaticMove.current = true
    try {
      if (view === 'all' && players.length) map.fitBounds([...players.map(p => p.coordinate), ...positions.teleports.flatMap(t => [t.from.coordinate, t.to.coordinate])], { padding: [35, 35], maxZoom: 4, animate: false })
      if (view === 'self' && self) map.setView(self.coordinate, map.getZoom(), { animate: false })
    } finally { automaticMove.current = false }
  }

  useEffect(() => {
    const close = () => { void bridge.closeTeamMap?.().catch(() => setCloseError(true)) }
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') close() }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [bridge])

  useEffect(() => {
    const tileUrl = resolvePrivateTileUrl('/map/tiles/{z}/{x}/{y}.png', config.baseUrl)
    if (!container.current || !tileUrl) { setTileError(true); return }
    setTileError(false)
    const map = L.map(container.current, {
      crs: L.CRS.Simple, attributionControl: false, zoomControl: true,
      minZoom: 0, maxZoom: 6, zoomAnimation: false, fadeAnimation: false,
      markerZoomAnimation: false, inertia: false,
    })
    mapRef.current = map
    teleportLayer.current = createTeleportLayer(map)
    map.setView([-128, 128], 1, { animate: false })
    const tiles = L.tileLayer(tileUrl, {
      bounds: PALWORLD_TILE_BOUNDS, noWrap: true, minNativeZoom: 0, maxNativeZoom: 6,
      updateWhenIdle: true, updateWhenZooming: false, keepBuffer: 1,
    })
    const onError = () => setTileError(true)
    tiles.on('tileerror', onError)
    tiles.addTo(map)
    const draw = () => {
      const { players, userID, stale } = latest.current
      const groups = groupTeamPositions(players, p => map.project(p.coordinate, map.getZoom()), userID)
      const wanted = new Set(groups.map(g => g.key))
      for (const [key, marker] of markers.current) if (!wanted.has(key)) { marker.remove(); markers.current.delete(key) }
      for (const group of groups) {
        let marker = markers.current.get(group.key)
        const color = group.self ? '#70e2d2' : '#edbd73'
        const style = { color, fillColor: color, fillOpacity: stale ? 0.3 : 0.85, opacity: stale ? 0.5 : 1, weight: group.self ? 3 : 1 }
        const label = `${group.players.length > 1 ? `${group.players.length}人 · ` : ''}${group.label}`
        const details = group.players.map(p => `${p.name}${p.user_id === userID ? '（自己）' : ''}`).join(' / ')
        if (!marker) {
          marker = L.circleMarker(group.coordinate, { ...style, radius: group.players.length > 1 ? 10 : 6 })
          marker.bindTooltip(textNode(label), { permanent: true, direction: 'top', className: 'team-map-label' })
          marker.bindPopup(textNode(details))
          marker.addTo(map)
          markers.current.set(group.key, marker)
        } else {
          marker.setLatLng(group.coordinate); marker.setStyle(style)
          marker.setRadius(group.players.length > 1 ? 10 : 6)
          marker.setTooltipContent(textNode(label)); marker.setPopupContent(textNode(details))
        }
      }
    }
    redraw.current = draw
    const manual = () => setView('manual')
    const zoom = () => { if (!automaticMove.current) manual() }
    map.on('dragstart', manual)
    map.on('zoomstart', zoom)
    map.on('zoomend', draw)
    const resize = () => { map.invalidateSize({ animate: false, pan: false }); updateCamera.current() }
    const observer = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(resize) : null
    observer?.observe(container.current)
    window.addEventListener('resize', resize)
    draw()
    return () => {
      observer?.disconnect(); window.removeEventListener('resize', resize)
      tiles.off('tileerror', onError); map.off('dragstart', manual); map.off('zoomstart', zoom); map.off('zoomend', draw)
      teleportLayer.current?.destroy(); teleportLayer.current = null
      markers.current.clear(); map.remove(); mapRef.current = null; redraw.current = () => {}
    }
  }, [config.baseUrl])

  useEffect(() => {
    const map = mapRef.current
    if (!map) return
    updateCamera.current()
    redraw.current()
  }, [players, config.userId, config.baseUrl, view, focus, positions.stale, self])

  useEffect(() => {
    teleportLayer.current?.update(positions.teleports)
    updateCamera.current()
  }, [positions.teleports, config.baseUrl])

  const age = Number.isFinite(positions.age) ? `${Math.floor(positions.age / 1000)} 秒前` : '等待观测'
  const chooseView = (next: View) => { setView(next); setFocus(v => v + 1) }
  return <main className="team-map-shell">
    <header className="team-map-header">
      <div><span className="team-map-eyebrow">PALREST / FIELD MAP</span><h1>全员地图</h1></div>
      <button className="team-map-close" onClick={() => void bridge.closeTeamMap?.().catch(() => setCloseError(true))} aria-label="关闭地图">关闭 <kbd>Esc</kbd></button>
    </header>
    <nav className="team-map-toolbar" aria-label="地图视野">
      <button aria-pressed={view === 'all'} onClick={() => chooseView('all')}>查看全员</button>
      <button aria-pressed={view === 'self'} disabled={!self} onClick={() => chooseView('self')}>跟随自己</button>
      <span>{view === 'manual' ? '自由浏览' : view === 'self' ? '跟随中' : '全员视野'}</span>
    </nav>
    <div className="team-map-stage">
      <div ref={container} className="team-map-canvas" aria-label="在线玩家位置地图" />
      {!positions.data ? <p className="team-map-notice" role="status">{positions.unavailable ? '请更新桌面客户端以使用全员地图' : positions.error ?? '正在读取全员位置…'}</p> : players.length === 0 ? <p className="team-map-notice" role="status">当前没有可显示的玩家位置</p> : null}
      {positions.teleports.length ? <div className="team-map-teleport-legend">⇢ 疑似传送 {positions.teleports.length} 次 · 保留 30 秒</div> : null}
      {tileError ? <p className="team-map-tile-error" role="status">底图未能完整加载，玩家位置仍可查看</p> : null}
    </div>
    <div className="team-map-roster" aria-label="有位置的玩家">
      {players.map(player => <button key={player.user_id} className={player.user_id === config.userId ? 'is-self' : ''} onClick={() => { setView('manual'); mapRef.current?.setView(player.coordinate, Math.max(3, mapRef.current.getZoom()), { animate: false }) }}>
        <i />{player.name}{player.user_id === config.userId ? ' · 自己' : ''}
      </button>)}
    </div>
    <footer className={`team-map-status${positions.stale ? ' is-stale' : ''}`} role="status">
      <span>{positions.data ? `${positions.data.online_count} 人在线 · ${players.length} 人有位置` : '等待服务器'}</span>
      <span>{closeError ? '关闭失败，请使用窗口关闭按钮' : !visible ? '已暂停刷新' : positions.error ? `连接中断 · 最后观测 ${age}` : positions.stale && positions.data ? `位置已过期 · ${age}` : `观测于 ${age}`}</span>
    </footer>
  </main>
}
export default TeamMap
