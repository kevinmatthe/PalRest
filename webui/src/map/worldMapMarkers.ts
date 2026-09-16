import L from 'leaflet';
import { PALWORLD_LANDSCAPE } from '../components/timelineShared';
import { TELEPORT_MIN_DIST } from '../behavior/behaviorTypes';

export type MapDisplayPoint = {
  user_id: string; name: string; x: number; y: number; level?: number;
  observedAt?: string; continuity: string; state: string;
};

/** The API carries game coordinates, including valid values close to zero. */
export function projectWorkspaceXY(x: number, y: number): [number, number] {
  const [maxX, maxY, minX, minY] = PALWORLD_LANDSCAPE;
  return [-256 + 256 * (x - minX) / (maxX - minX), 256 * (y - minY) / (maxY - minY)];
}

export function playerColor(id: string) {
  let hash = 2166136261;
  for (let i = 0; i < id.length; i++) hash = Math.imul(hash ^ id.charCodeAt(i), 16777619);
  return `hsl(${(hash >>> 0) % 360} 72% 68%)`;
}

type Entry = { marker: L.Marker; point: MapDisplayPoint; title: HTMLElement; detail: HTMLElement; badge: HTMLElement; frame: number; stale: boolean };

/** Owns markers independently from React rendering; all user text uses textContent. */
export class WorldMapMarkers {
  private entries = new Map<string, Entry>();
  constructor(private map: L.Map, private onSelect: (id: string) => void) {}

  sync(points: MapDisplayPoint[], selectedID: string, mode: 'live' | 'history', reduced: boolean, stale: boolean) {
    const ids = new Set(points.map(p => p.user_id));
    for (const [id, entry] of this.entries) {
      if (!ids.has(id)) { cancelAnimationFrame(entry.frame); entry.marker.remove(); this.entries.delete(id); }
    }
    for (const p of points) {
      if (!Number.isFinite(p.x) || !Number.isFinite(p.y)) continue;
      let entry = this.entries.get(p.user_id);
      if (!entry) {
        const icon = document.createElement('span');
        icon.className = 'world-pin-body';
        icon.style.setProperty('--player-color', playerColor(p.user_id));
        const badge = document.createElement('span');
        badge.className = 'world-pin-initial';
        badge.textContent = Array.from(p.name.trim()).slice(0, 1).join('') || '?';
        icon.append(badge);
        const marker = L.marker(projectWorkspaceXY(p.x, p.y), {
          icon: L.divIcon({ className: 'world-player-pin', html: icon, iconSize: [32, 32], iconAnchor: [16, 16] }),
          title: p.name, keyboard: true,
        }).addTo(this.map);
        const card = document.createElement('div');
        const title = document.createElement('strong');
        const detail = document.createElement('span');
        card.className = 'world-marker-card';
        card.append(title, detail);
        marker.bindTooltip(card, { permanent: false, direction: 'top', offset: [0, -22], className: 'world-player-tooltip', opacity: 1 });
        marker.on('click', () => this.onSelect(p.user_id));
        entry = { marker, point: p, title, detail, badge, frame: 0, stale };
        this.entries.set(p.user_id, entry);
      }
      const active = p.user_id === selectedID;
      const label = mode === 'live' ? (stale ? '最后观测' : '当前位置') : p.state;
      entry.title.textContent = p.name;
      entry.badge.textContent = Array.from(p.name.trim()).slice(0, 1).join('') || '?';
      const stamp = p.observedAt ? new Date(p.observedAt) : null;
      const observed = stamp && Number.isFinite(stamp.getTime()) ? stamp.toLocaleTimeString('zh-CN', { hour12: false }) : '时间未知';
      const detail = `${label} · ${p.level ? `Lv.${p.level} · ` : ''}${observed}`;
      if (entry.detail.textContent !== detail) entry.detail.textContent = detail;
      const el = entry.marker.getElement();
      el?.classList.toggle('is-selected', active);
      el?.classList.toggle('is-stale', stale || p.state === '观测缺口' || p.state === '最后观测');
      el?.setAttribute('aria-label', `选择玩家 ${p.name}`);
      el?.setAttribute('title', p.name);
      entry.marker.setZIndexOffset(active ? 1000 : 0);
      const tooltip = entry.marker.getTooltip();
      if (tooltip) tooltip.options.permanent = active;
      if (active) entry.marker.openTooltip();
      else if (entry.marker.isTooltipOpen()) entry.marker.closeTooltip();
      const prev = entry.point;
      const changed = prev.x !== p.x || prev.y !== p.y;
      const dt = Date.parse(p.observedAt ?? '') - Date.parse(prev.observedAt ?? '');
      const animate = mode === 'live' && !reduced && !stale && !entry.stale && prev.continuity === p.continuity && dt > 0 && dt <= 60000
        && Math.hypot(p.x - prev.x, p.y - prev.y) < TELEPORT_MIN_DIST;
      if (changed || prev.continuity !== p.continuity) {
        cancelAnimationFrame(entry.frame);
        const to = L.latLng(projectWorkspaceXY(p.x, p.y));
        if (!animate) entry.marker.setLatLng(to);
        else {
          const from = entry.marker.getLatLng();
          const started = performance.now();
          const target = entry;
          const tick = (now: number) => {
            const t = Math.min(1, (now - started) / 550);
            const eased = t * t * (3 - 2 * t);
            target.marker.setLatLng([from.lat + (to.lat - from.lat) * eased, from.lng + (to.lng - from.lng) * eased]);
            if (t < 1) target.frame = requestAnimationFrame(tick);
          };
          entry.frame = requestAnimationFrame(tick);
        }
      }
      if (reduced || stale) { cancelAnimationFrame(entry.frame); entry.marker.setLatLng(projectWorkspaceXY(p.x, p.y)); }
      entry.point = p;
      entry.stale = stale;
    }
  }

  clear() { for (const e of this.entries.values()) { cancelAnimationFrame(e.frame); e.marker.remove(); } this.entries.clear(); }
}
