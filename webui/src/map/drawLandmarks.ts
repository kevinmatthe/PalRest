import L from 'leaflet';
import type { WorldPOI } from '../api';
import { projectWorldXY } from '../components/timelineShared';
import type { MapLandmark } from './mapLandmarks';

export function drawStaticLandmarks(layer: L.LayerGroup, landmarks: MapLandmark[]) {
  landmarks.forEach((lm) => {
    const marker = L.circleMarker(projectWorldXY(lm.x, lm.y), {
      radius: lm.kind === 'boss_tower' ? 5 : 3,
      color: lm.kind === 'boss_tower' ? '#8d5a0f' : '#3d6b7a',
      fillColor: lm.kind === 'boss_tower' ? '#e8b86d' : '#9fd0db',
      fillOpacity: 0.85,
      weight: 1.5,
      opacity: 0.9,
      className: `map-landmark map-landmark--${lm.kind}`,
    });
    marker.bindTooltip(lm.nameZh, { direction: 'top', opacity: 0.92 });
    marker.addTo(layer);
  });
}

/** Distinct house/flag style markers for guild bases (not circles). */
export function drawGuildBaseLandmarks(layer: L.LayerGroup, bases: WorldPOI[]) {
  bases.forEach((poi) => {
    const icon = L.divIcon({
      className: 'map-landmark-guild-icon',
      html: '<span class="map-landmark-guild-glyph" aria-hidden="true">⌂</span>',
      iconSize: [20, 20],
      iconAnchor: [10, 10],
    });
    const marker = L.marker(projectWorldXY(poi.x, poi.y), {
      icon,
      keyboard: false,
      interactive: true,
    });
    const guild = poi.guild_name ? `公会「${poi.guild_name}」` : '公会据点';
    marker.bindTooltip(`${guild} · ${poi.name_zh}`, { direction: 'top', opacity: 0.95 });
    marker.addTo(layer);
  });
}
