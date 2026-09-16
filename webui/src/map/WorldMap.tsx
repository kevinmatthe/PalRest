import { memo, useEffect, useRef, useState } from 'react';
import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import type { WorldPOI } from '../api';
import { PALWORLD_TILE_BOUNDS, PALWORLD_TILE_URL, PALWORLD_TILE_FALLBACK_URL } from '../components/timelineShared';
import { MAP_LANDMARKS } from './mapLandmarks';
import { WorldMapMarkers, projectWorkspaceXY, type MapDisplayPoint } from './worldMapMarkers';
import { trajectoryRuns, type PreparedSample } from './workspacePlayback';
import { disposeLeafletMap } from './disposeLeafletMap';

export type WorldMapProps = {
  mode: 'live' | 'history'; points: MapDisplayPoint[]; selectedID: string;
  samples: PreparedSample[]; cursorTime: number; showTrail: boolean; showLandmarks: boolean;
  showBases: boolean; bases: WorldPOI[]; follow: boolean; focusRequest: number; stale: boolean;
  onSelect: (id: string) => void; onInteraction: () => void;
};

export const WorldMap = memo(function WorldMap(props: WorldMapProps) {
  const root = useRef<HTMLDivElement>(null);
  const mapRef = useRef<L.Map | null>(null);
  const markerRef = useRef<WorldMapMarkers | null>(null);
  const landmarksRef = useRef<L.LayerGroup | null>(null);
  const trailRef = useRef<L.Polyline | null>(null);
  const cursorTrailRef = useRef<L.Polyline | null>(null);
  const propsRef = useRef(props);
  propsRef.current = props;
  const reducedRef = useRef(false);
  const [reduced, setReduced] = useState(false);
  const [tileError, setTileError] = useState(false);
  const lastFocus = useRef(0);
  const lastPan = useRef(0);
  const lastTrailIndex = useRef(-2);

  useEffect(() => {
    const media = window.matchMedia?.('(prefers-reduced-motion: reduce)');
    const update = () => { reducedRef.current = media?.matches ?? false; setReduced(reducedRef.current); };
    update();
    media?.addEventListener('change', update);
    return () => media?.removeEventListener('change', update);
  }, []);

  useEffect(() => {
    if (!root.current) return;
    const map = L.map(root.current, {
      attributionControl: false, zoomControl: false, crs: L.CRS.Simple,
      minZoom: 0, maxZoom: 6, zoomSnap: 0.5, maxBounds: PALWORLD_TILE_BOUNDS, maxBoundsViscosity: 0.8,
      center: [-128, 128], zoom: 2,
    });
    const tiles = L.tileLayer(PALWORLD_TILE_URL, { bounds: PALWORLD_TILE_BOUNDS, noWrap: true, minNativeZoom: 0, maxNativeZoom: 6 });
    tiles.on('tileerror', (event: L.TileErrorEvent) => {
      const tile = event.tile as HTMLImageElement;
      if (tile.dataset.fallback) { setTileError(true); return; }
      tile.dataset.fallback = 'true';
      tile.src = L.Util.template(PALWORLD_TILE_FALLBACK_URL, event.coords);
    });
    tiles.addTo(map);
    L.control.zoom({ position: 'topleft', zoomInTitle: '放大地图', zoomOutTitle: '缩小地图' }).addTo(map);
    map.fitBounds(PALWORLD_TILE_BOUNDS, { animate: false });
    mapRef.current = map;
    markerRef.current = new WorldMapMarkers(map, id => propsRef.current.onSelect(id));
    landmarksRef.current = L.layerGroup().addTo(map);
    trailRef.current = L.polyline([], { color: '#71ddcf', weight: 3, opacity: 0.72, interactive: false }).addTo(map);
    cursorTrailRef.current = L.polyline([], { color: '#dcfbe8', weight: 4, interactive: false }).addTo(map);
    const stopFollow = () => propsRef.current.onInteraction();
    map.on('dragstart', stopFollow);
    const manual = () => { map.stop(); stopFollow(); };
    const key = (e: KeyboardEvent) => { if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', '+', '-', '='].includes(e.key)) manual(); };
    const element = root.current;
    element.addEventListener('wheel', manual, { passive: true });
    element.addEventListener('keydown', key);
    // Pointer input cancels an in-flight camera pan before a drag begins.
    element.addEventListener('pointerdown', manual);
    const resize = new ResizeObserver(() => map.invalidateSize({ animate: false }));
    resize.observe(element);
    const frame = requestAnimationFrame(() => map.invalidateSize({ animate: false }));
    return () => {
      cancelAnimationFrame(frame); resize.disconnect();
      element.removeEventListener('wheel', manual); element.removeEventListener('keydown', key); element.removeEventListener('pointerdown', manual);
      markerRef.current?.clear(); markerRef.current = null;
      disposeLeafletMap(map); mapRef.current = null; landmarksRef.current = null; trailRef.current = null; cursorTrailRef.current = null;
    };
  }, []);

  useEffect(() => {
    markerRef.current?.sync(props.points, props.selectedID, props.mode, reduced, props.stale);
    const map = mapRef.current;
    const point = props.points.find(p => p.user_id === props.selectedID);
    if (!map || !point) return;
    const location = projectWorkspaceXY(point.x, point.y);
    if (lastFocus.current !== props.focusRequest) {
      lastFocus.current = props.focusRequest;
      map.setView(location, Math.max(map.getZoom(), 3), { animate: !reduced, duration: 0.55 });
      lastPan.current = performance.now();
    } else if (props.follow && performance.now() - lastPan.current > 700 && !map.getBounds().pad(-0.3).contains(location)) {
      map.panTo(location, { animate: !reduced, duration: 0.55 });
      lastPan.current = performance.now();
    }
  }, [props.points, props.selectedID, props.mode, props.follow, props.focusRequest, props.stale, reduced]);

  useEffect(() => {
    const layer = landmarksRef.current;
    if (!layer) return;
    layer.clearLayers();
    if (props.showLandmarks) for (const lm of MAP_LANDMARKS) {
      const label = document.createElement('span'); label.textContent = lm.nameZh;
      L.circleMarker(projectWorkspaceXY(lm.x, lm.y), { radius: lm.kind === 'boss_tower' ? 5 : 3, color: '#ddc689', fillOpacity: 0.8, weight: 1 })
        .bindTooltip(label).addTo(layer);
    }
    if (props.showBases) for (const base of props.bases) {
      const label = document.createElement('span'); label.textContent = base.name_zh;
      L.marker(projectWorkspaceXY(base.x, base.y), { icon: L.divIcon({ className: 'world-base-pin', html: '⌂', iconSize: [24, 24], iconAnchor: [12, 12] }) })
        .bindTooltip(label).addTo(layer);
    }
  }, [props.showLandmarks, props.showBases, props.bases]);

  useEffect(() => { lastTrailIndex.current = -2; }, [props.samples, props.showTrail, props.mode]);
  useEffect(() => {
    const trail = trailRef.current;
    const cursorTrail = cursorTrailRef.current;
    if (!trail || !cursorTrail) return;
    if (!props.showTrail || props.mode !== 'history') { trail.setLatLngs([]); cursorTrail.setLatLngs([]); return; }
    let index = -1;
    while (index + 1 < props.samples.length && props.samples[index + 1].time <= props.cursorTime) index++;
    if (index !== lastTrailIndex.current) {
      const runs = trajectoryRuns(props.samples.slice(0, index + 1));
      trail.setLatLngs(runs.map(run => run.map(p => projectWorkspaceXY(p.x, p.y))));
      lastTrailIndex.current = index;
    }
    const point = props.points[0];
    const anchor = props.samples[index];
    cursorTrail.setLatLngs(point && anchor && point.state === '回放位置 · 插值' ? [projectWorkspaceXY(anchor.x, anchor.y), projectWorkspaceXY(point.x, point.y)] : []);
  }, [props.samples, props.points, props.cursorTime, props.showTrail, props.mode]);

  return <>
    <div ref={root} className="world-map-canvas" aria-label="帕鲁世界地图" data-testid="world-map" />
    {tileError ? <div className="world-tile-error" role="status">部分地图未能加载，玩家位置仍可查看</div> : null}
  </>;
});
