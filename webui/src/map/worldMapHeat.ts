import L from 'leaflet';
import type { JourneyHeatCell } from './journeyTypes';
import { projectWorkspaceXY } from './worldMapMarkers';

type Entry = { marker: L.CircleMarker; label: HTMLDivElement; cell: JourneyHeatCell };

/** Stable map objects; visual weight represents observed seconds, never hits. */
export class WorldMapHeat {
  private entries = new Map<string, Entry>();
  constructor(private layer: L.LayerGroup, private onSelect: (cell: JourneyHeatCell) => void) {}
  sync(cells: JourneyHeatCell[]) {
    const seen = new Set<string>();
    for (const cell of cells) {
      if (![cell.x, cell.y, cell.durationMs, cell.start, cell.end].every(Number.isFinite) || cell.durationMs <= 0 || cell.end <= cell.start) continue;
      seen.add(cell.id);
      let entry = this.entries.get(cell.id);
      if (!entry) {
        const label = document.createElement('div');
        const marker = L.circleMarker(projectWorkspaceXY(cell.x, cell.y), { color: '#ecd29c', fillColor: '#eeb779', weight: 1, className: 'world-dwell-cell' });
        entry = { marker, label, cell };
        const current = entry;
        marker.bindTooltip(label).on('click', () => this.onSelect(current.cell)).addTo(this.layer);
        this.entries.set(cell.id, entry);
      }
      entry.cell = cell;
      entry.marker.setLatLng(projectWorkspaceXY(cell.x, cell.y));
      const weight = Math.log1p(cell.durationMs / 60000);
      entry.marker.setRadius(8 + Math.min(22, weight * 6));
      entry.marker.setStyle({ fillOpacity: Math.min(0.7, 0.2 + weight * 0.12) });
      entry.label.textContent = `停留观测 ${Math.round(cell.durationMs / 1000)} 秒 · 近似网格区域\n${new Date(cell.start).toLocaleString('zh-CN')} – ${new Date(cell.end).toLocaleString('zh-CN')}`;
    }
    for (const [id, entry] of this.entries) if (!seen.has(id)) { this.layer.removeLayer(entry.marker); this.entries.delete(id); }
  }
  clear() { this.entries.clear(); this.layer.clearLayers(); }
}
