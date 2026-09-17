import * as L from 'leaflet'
import type { TeamTeleport } from './teamTeleports'

/** Static vector arrows: repaint arrowheads on zoom only, never an animation loop. */
export function createTeleportLayer(map: L.Map) {
  const paths = new Map<string, { event: TeamTeleport; line: L.Polyline; head: L.Polyline }>()
  function arrowhead(event: TeamTeleport): L.LatLngExpression[] {
    const from = map.project(event.from.coordinate, map.getZoom())
    const to = map.project(event.to.coordinate, map.getZoom())
    const length = Math.hypot(to.x - from.x, to.y - from.y)
    if (!length) return []
    const dx = (to.x - from.x) / length, dy = (to.y - from.y) / length
    const size = Math.min(11, length / 3)
    return [map.unproject([to.x - size * dx - size * .5 * dy, to.y - size * dy + size * .5 * dx], map.getZoom()),
      event.to.coordinate,
      map.unproject([to.x - size * dx + size * .5 * dy, to.y - size * dy - size * .5 * dx], map.getZoom())]
  }
  const zoom = () => { for (const path of paths.values()) path.head.setLatLngs(arrowhead(path.event)) }
  map.on('zoomend', zoom)
  const update = (events: readonly TeamTeleport[]) => {
    const wanted = new Set(events.map(event => event.id))
    for (const [id, path] of paths) if (!wanted.has(id)) { path.line.remove(); path.head.remove(); paths.delete(id) }
    for (const event of events) {
      if (paths.has(event.id)) continue
      const label = document.createElement('span')
      label.textContent = `${event.playerName} · 疑似传送：${event.from.name} → ${event.to.name}`
      const line = L.polyline([event.from.coordinate, event.to.coordinate], { color: '#c2b4ff', weight: 2, opacity: .85, dashArray: '6 6' })
      line.bindTooltip(label)
      line.addTo(map)
      const head = L.polyline(arrowhead(event), { color: '#c2b4ff', weight: 2, opacity: .95, interactive: false })
      head.addTo(map)
      paths.set(event.id, { event, line, head })
    }
  }
  return { update, destroy: () => { map.off('zoomend', zoom); update([]) } }
}
