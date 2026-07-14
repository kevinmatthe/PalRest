import { useEffect, useMemo, useRef, useState } from 'react';
import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import 'leaflet.markercluster';
import 'leaflet.markercluster/dist/MarkerCluster.css';
import { Map as MapIcon, Route } from 'lucide-react';
import { PLAY_SPEEDS, type PlaySpeed } from '../map/mapPlayback';
import { hybridTrajectoryWindow, pingColor } from '../map/mapTrajectory';
import { mergeTimelineTicks, timelinePercent } from '../map/timelineTicks';
import { shouldPanToFocus } from '../map/mapView';
import {
  eventLabel,
  formatTimelineDateTime,
  latestPointAt,
  mapLocationLabel,
  PALWORLD_TILE_BOUNDS,
  PALWORLD_TILE_FALLBACK_URL,
  PALWORLD_TILE_URL,
  projectWorldSample,
  trajectoryKey,
  trajectorySamples,
  type LogItem,
  type TrajectorySample,
} from './timelineShared';

type MapPoint = { key: string; sample: TrajectorySample; latLng: L.LatLngExpression };

const PING_LEGEND: Array<{ label: string; fill: string }> = [
  { label: '<50', fill: pingColor(20).fill },
  { label: '50–80', fill: pingColor(60).fill },
  { label: '80–120', fill: pingColor(100).fill },
  { label: '120–200', fill: pingColor(150).fill },
  { label: '>200', fill: pingColor(250).fill },
  { label: '未知', fill: pingColor(Number.NaN).fill },
];

export function tileErrorTransition(alreadyFellBack: boolean, coords: { x: number; y: number; z: number }): { action: 'retry'; src: string } | { action: 'fail' } {
  if (alreadyFellBack) return { action: 'fail' };
  return { action: 'retry', src: L.Util.template(PALWORLD_TILE_FALLBACK_URL, coords) };
}

const FallbackTileLayer = L.TileLayer.extend({
  createTile(this: L.TileLayer, coords: L.Coords, done: L.DoneCallback) {
    const tile = document.createElement('img');
    tile.setAttribute('role', 'presentation');
    tile.alt = '';
    L.DomEvent.on(tile, 'load', L.Util.bind((this as unknown as { _tileOnLoad: (cb: L.DoneCallback, t: HTMLImageElement) => void })._tileOnLoad, this, done, tile));
    tile.onerror = () => {
      const transition = tileErrorTransition(tile.dataset.fellBack === 'true', { x: coords.x, y: coords.y, z: this._getZoomForUrl() });
      if (transition.action === 'fail') {
        (this as unknown as { _tileOnError: (cb: L.DoneCallback, t: HTMLImageElement, e: Error) => void })._tileOnError(done, tile, new Error('tile unavailable'));
        return;
      }
      tile.dataset.fellBack = 'true';
      tile.src = transition.src;
    };
    if (this.options.crossOrigin || this.options.crossOrigin === '') {
      tile.crossOrigin = this.options.crossOrigin === true ? '' : this.options.crossOrigin;
    }
    tile.src = this.getTileUrl(coords);
    return tile;
  },
}) as unknown as new (url: string, options?: L.TileLayerOptions) => L.TileLayer;

export type TimelineMapProps = {
  activeIndex: number;
  items: LogItem[];
  loading: boolean;
  onCursorChange: (index: number) => void;
  onSeekTrajectory: (sample: TrajectorySample) => void;
  onStep: (direction: -1 | 1) => void;
  playing: boolean;
  speed: PlaySpeed;
  onPlayingChange: (playing: boolean) => void;
  onSpeedChange: (speed: PlaySpeed) => void;
  selected: boolean;
};

export function TimelineMap({
  activeIndex,
  items,
  loading,
  onCursorChange,
  onSeekTrajectory,
  onStep,
  playing,
  speed,
  onPlayingChange,
  onSpeedChange,
  selected,
}: TimelineMapProps) {
  const mapElementRef = useRef<HTMLDivElement>(null);
  const trackRef = useRef<HTMLDivElement>(null);
  const leafletRef = useRef<L.Map | null>(null);
  const clusterGroupRef = useRef<L.MarkerClusterGroup | null>(null);
  const lineLayerRef = useRef<L.LayerGroup | null>(null);
  const focusLayerRef = useRef<L.LayerGroup | null>(null);
  const tileLayerRef = useRef<L.TileLayer | null>(null);
  const markersByKeyRef = useRef(new Map<string, L.CircleMarker>());
  const excludedClusterKeyRef = useRef('');
  const onSeekTrajectoryRef = useRef(onSeekTrajectory);
  onSeekTrajectoryRef.current = onSeekTrajectory;
  const samples = useMemo(() => trajectorySamples(items), [items]);
  const [mapAvailable, setMapAvailable] = useState(true);
  const [colorMode, setColorMode] = useState<'position' | 'ping'>('position');
  const [trackWidthPx, setTrackWidthPx] = useState(320);
  const points = useMemo<MapPoint[]>(() => {
    return samples.map((sample) => {
      return { key: trajectoryKey(sample), latLng: projectWorldSample(sample), sample };
    });
  }, [samples]);
  const activeItem = items[activeIndex];
  const activeSample = latestPointAt(samples, activeItem?.at);
  const activeSampleKey = activeSample ? trajectoryKey(activeSample) : '';
  const startMS = items.length ? Math.min(...items.map((item) => Date.parse(item.at))) : 0;
  const endMS = items.length ? Math.max(...items.map((item) => Date.parse(item.at))) : 1;
  const cursorLeft = activeItem ? timelinePercent(activeItem.at, startMS, endMS) : 0;
  const activeLabel = activeItem ? activeItem.kind === 'event' ? `光标：${eventLabel(activeItem.item.event_type)}` : activeItem.kind === 'trajectory' ? '光标：位置采样' : '光标：私有玩家采样' : selected ? '等待观察记录' : '未选择玩家';
  const mergedTicks = useMemo(
    () => mergeTimelineTicks(items, startMS, endMS, trackWidthPx, activeIndex),
    [activeIndex, endMS, items, startMS, trackWidthPx],
  );

  useEffect(() => {
    const track = trackRef.current;
    if (!track || typeof ResizeObserver === 'undefined') return;
    const apply = () => setTrackWidthPx(Math.max(1, Math.floor(track.getBoundingClientRect().width)));
    apply();
    const ro = new ResizeObserver(apply);
    ro.observe(track);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    if (!mapElementRef.current || leafletRef.current) return;
    const map = L.map(mapElementRef.current, {
      attributionControl: false,
      crs: L.CRS.Simple,
      center: [-128, 128],
      maxBounds: PALWORLD_TILE_BOUNDS,
      maxBoundsViscosity: 0.8,
      minZoom: 0,
      maxZoom: 6,
      zoom: 2,
      zoomControl: true,
    });
    const tileLayer = new FallbackTileLayer(PALWORLD_TILE_URL, {
      bounds: PALWORLD_TILE_BOUNDS,
      maxNativeZoom: 6,
      minNativeZoom: 0,
      noWrap: true,
      tileSize: 256,
    });
    tileLayer.on('tileerror', () => setMapAvailable(false));
    tileLayer.addTo(map);
    map.fitBounds(PALWORLD_TILE_BOUNDS);
    const clusterGroup = L.markerClusterGroup({
      showCoverageOnHover: false,
      iconCreateFunction(cluster) {
        const count = cluster.getChildCount();
        const size = count < 10 ? 'sm' : count < 50 ? 'md' : 'lg';
        return L.divIcon({
          html: `<div><span>${count}</span></div>`,
          className: `timeline-marker-cluster timeline-marker-cluster--${size}`,
          iconSize: L.point(size === 'sm' ? 36 : size === 'md' ? 44 : 52, size === 'sm' ? 36 : size === 'md' ? 44 : 52),
        });
      },
    });
    const lineLayer = L.layerGroup();
    const focusLayer = L.layerGroup();
    clusterGroup.addTo(map);
    lineLayer.addTo(map);
    focusLayer.addTo(map);
    leafletRef.current = map;
    tileLayerRef.current = tileLayer;
    clusterGroupRef.current = clusterGroup;
    lineLayerRef.current = lineLayer;
    focusLayerRef.current = focusLayer;
    return () => {
      map.remove();
      leafletRef.current = null;
      tileLayerRef.current = null;
      clusterGroupRef.current = null;
      lineLayerRef.current = null;
      focusLayerRef.current = null;
      markersByKeyRef.current.clear();
      excludedClusterKeyRef.current = '';
    };
  }, []);

  useEffect(() => {
    const clusterGroup = clusterGroupRef.current;
    if (!clusterGroup) return;
    clusterGroup.clearLayers();
    markersByKeyRef.current.clear();
    excludedClusterKeyRef.current = '';
    points.forEach((point) => {
      const colors = colorMode === 'ping'
        ? pingColor(point.sample.ping)
        : { fill: '#fffdf7', stroke: '#0f7285' };
      const marker = L.circleMarker(point.latLng, {
        radius: 4,
        color: colors.stroke,
        fillColor: colors.fill,
        fillOpacity: 1,
        weight: 2,
      });
      marker.on('click', () => onSeekTrajectoryRef.current(point.sample));
      markersByKeyRef.current.set(point.key, marker);
      clusterGroup.addLayer(marker);
    });
  }, [colorMode, points]);

  useEffect(() => {
    const map = leafletRef.current;
    const clusterGroup = clusterGroupRef.current;
    const lineLayer = lineLayerRef.current;
    const focusLayer = focusLayerRef.current;
    if (!map || !clusterGroup || !lineLayer || !focusLayer) return;

    const previousKey = excludedClusterKeyRef.current;
    if (previousKey && previousKey !== activeSampleKey) {
      const previous = markersByKeyRef.current.get(previousKey);
      if (previous && !clusterGroup.hasLayer(previous)) clusterGroup.addLayer(previous);
    }
    if (activeSampleKey) {
      const active = markersByKeyRef.current.get(activeSampleKey);
      if (active && clusterGroup.hasLayer(active)) clusterGroup.removeLayer(active);
    }
    excludedClusterKeyRef.current = activeSampleKey;

    lineLayer.clearLayers();
    const activeAt = activeItem?.at;
    const lineSamples = hybridTrajectoryWindow(samples, activeAt);
    if (lineSamples.length > 1) {
      L.polyline(lineSamples.map((sample) => projectWorldSample(sample)), {
        color: '#0f7285',
        opacity: 0.88,
        weight: 2,
        lineCap: 'round',
        lineJoin: 'round',
      }).addTo(lineLayer);
    }

    focusLayer.clearLayers();
    const focusSample = latestPointAt(samples, activeAt);
    if (focusSample) {
      const activePoint = projectWorldSample(focusSample);
      const ping = colorMode === 'ping' ? pingColor(focusSample.ping) : null;
      L.circleMarker(activePoint, {
        radius: 8,
        color: '#8d5a0f',
        fillColor: ping?.fill ?? '#ca8519',
        fillOpacity: 0.92,
        weight: 3,
      }).addTo(focusLayer);
      if (shouldPanToFocus(map.getBounds(), activePoint)) {
        map.panTo(activePoint, { animate: false });
      }
    }
  }, [activeItem?.at, activeSampleKey, colorMode, samples]);

  return <section className="timeline-map-panel" aria-label="地图回放">
    <div className="timeline-map-header">
      <div>
        <p className="eyebrow">地图回放</p>
        <h3>{activeLabel}</h3>
      </div>
      <div className="timeline-map-header-actions">
        <div className="timeline-color-mode" role="group" aria-label="点位颜色模式">
          <button type="button" aria-pressed={colorMode === 'position'} onClick={() => setColorMode('position')}>位置</button>
          <button type="button" aria-pressed={colorMode === 'ping'} onClick={() => setColorMode('ping')}>延迟</button>
        </div>
        <span className="timeline-map-count"><Route size={15} /> {samples.length} 个坐标</span>
      </div>
    </div>
    {colorMode === 'ping' ? (
      <div className="timeline-ping-legend" aria-label="延迟图例">
        <span className="timeline-ping-legend-title">延迟</span>
        {PING_LEGEND.map((entry) => (
          <span className="timeline-ping-legend-item" key={entry.label}>
            <span className="timeline-ping-swatch" style={{ background: entry.fill }} aria-hidden="true" />
            {entry.label}
          </span>
        ))}
      </div>
    ) : null}
    <div className="timeline-map-surface">
      <div aria-label="Palworld 完整游戏地图" className="timeline-leaflet-map" data-testid="timeline-map" ref={mapElementRef} role="img" />
      {!mapAvailable ? <div className="timeline-map-missing" role="status">真实地图瓦片加载失败：本地 <code>webui/public/map/tiles</code> 与 palworld.gg 均无法读取，请检查 Git LFS 或网络。</div> : null}
      {!points.length ? <div className="timeline-map-empty">{loading ? '正在加载轨迹证据。' : selected ? '当前时间范围没有位置样本，已显示完整地图。' : '选择玩家后叠加轨迹。'}</div> : null}
    </div>
    <div className="timeline-cursor">
      <div className="timeline-cursor-track" aria-hidden="true" ref={trackRef} data-testid="timeline-cursor-track">
        {mergedTicks.map((tick) => (
          <span
            className={`timeline-cursor-tick timeline-cursor-tick--${tick.kind}`}
            key={tick.key}
            style={{ left: `${tick.leftPercent}%` }}
            data-active={tick.active ? 'true' : undefined}
            data-count={tick.count > 1 ? tick.count : undefined}
            title={tick.count > 1 ? `${tick.count} 条记录` : undefined}
          />
        ))}
        <span className="timeline-cursor-now" style={{ left: `${cursorLeft}%` }} />
      </div>
      <div className="timeline-transport">
        <button type="button" aria-label="上一步" disabled={items.length < 2 || activeIndex <= 0} onClick={() => onStep(-1)}>上一步</button>
        <button
          type="button"
          aria-label={playing ? '暂停' : '播放'}
          disabled={items.length < 2}
          onClick={() => onPlayingChange(!playing)}
        >
          {playing ? '暂停' : '播放'}
        </button>
        <button type="button" aria-label="下一步" disabled={items.length < 2 || activeIndex >= items.length - 1} onClick={() => onStep(1)}>下一步</button>
        <label>
          <span className="sr-only">倍速</span>
          <select
            aria-label="播放倍速"
            value={speed}
            disabled={items.length < 2}
            onChange={(event) => onSpeedChange(Number(event.target.value) as PlaySpeed)}
          >
            {PLAY_SPEEDS.map((value) => <option key={value} value={value}>{value}×</option>)}
          </select>
        </label>
        <input aria-label="时间轴光标" disabled={items.length < 2} max={Math.max(items.length - 1, 0)} min={0} onChange={(event) => onCursorChange(Number(event.target.value))} type="range" value={activeIndex} />
      </div>
      <div className="timeline-map-meta">
        <span><MapIcon size={15} /> {activeSample ? mapLocationLabel(activeSample) : '无地图位置'}</span>
        <span>{activeItem ? formatTimelineDateTime(activeItem.at) : '无光标时间'}</span>
      </div>
    </div>
  </section>;
}
