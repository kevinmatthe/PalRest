import { useEffect, useMemo, useRef, useState } from 'react';
import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import 'leaflet.markercluster';
import 'leaflet.markercluster/dist/MarkerCluster.css';
import { Map as MapIcon, Route } from 'lucide-react';
import { edgeClassBetween } from '../behavior/behaviorMetrics';
import { BEHAVIOR_EDGE_COLORS, type BehaviorSummary, type TeleportSuspect } from '../behavior/behaviorTypes';
import {
  clearExpandState,
  expandClusterForExactPoints,
  type ExpandState,
} from '../map/mapClusterReveal';
import { getGuildBases, type WorldPOI } from '../api';
import { drawGuildBaseLandmarks, drawStaticLandmarks } from '../map/drawLandmarks';
import { filterGuildBases } from '../map/guildLandmarks';
import { MAP_LANDMARKS } from '../map/mapLandmarks';
import { PLAY_SPEEDS, type PlayMode, type PlaySpeed } from '../map/mapPlayback';
import { LandmarkLayerControls } from './LandmarkLayerControls';
import {
  ARROW_EDGE_SIZE_PX,
  ARROW_SIZE_PX,
  BREATH_COLORS,
  collectBreathTargets,
  hybridTrajectoryWindow,
  pingColor,
  splitTrajectoryPastFuture,
  TRAJ_FUTURE_COLOR,
  TRAJ_FUTURE_DASH_ARRAY,
  TRAJ_FUTURE_OPACITY,
  TRAJ_FUTURE_WEIGHT,
  TRAJ_TIP_COLOR,
  TRAJ_TIP_WEIGHT,
} from '../map/mapTrajectory';
import { mergeTimelineTicks, timelinePercent } from '../map/timelineTicks';
import { bearingDeg, midpointLatLng, shouldPanToFocus, travelBearingEndpoints } from '../map/mapView';
import {
  eventLabel,
  formatTimelineDateTime,
  latestPointAt,
  mapLocationLabel,
  PALWORLD_LANDSCAPE,
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

const MAX_CLUSTER_RADIUS = 40;
const DISABLE_CLUSTERING_AT_ZOOM = 4;
/** Normalized cluster chip sizes (px) — no count labels by default. */
const CLUSTER_ICON_PX = { sm: 16, md: 18, lg: 20 } as const;

type SampleCircleMarker = L.CircleMarker & { options: L.CircleMarkerOptions & { sampleKey?: string } };

const PING_LEGEND: Array<{ label: string; fill: string; glyph: string }> = [
  { label: '<50', fill: pingColor(20).fill, glyph: pingColor(20).glyph },
  { label: '50–80', fill: pingColor(60).fill, glyph: pingColor(60).glyph },
  { label: '80–120', fill: pingColor(100).fill, glyph: pingColor(100).glyph },
  { label: '120–200', fill: pingColor(150).fill, glyph: pingColor(150).glyph },
  { label: '>200', fill: pingColor(250).fill, glyph: pingColor(250).glyph },
  { label: '未知', fill: pingColor(Number.NaN).fill, glyph: pingColor(Number.NaN).glyph },
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

function projectWorldXY(x: number, y: number): L.LatLngExpression {
  const [maxX, maxY, minX, minY] = PALWORLD_LANDSCAPE;
  if (x >= -256 && x <= 256) return [x, y];
  const mapX = -256 + (256 * (x - minX)) / (maxX - minX);
  const mapY = (256 * (y - minY)) / (maxY - minY);
  return [mapX, mapY];
}

/** World-space points to pan/fit the map to (from behavior panel clicks). */
export type MapFocusTarget = {
  key: string;
  points: Array<{ x: number; y: number }>;
};

export type TimelineMapProps = {
  activeIndex: number;
  items: LogItem[];
  loading: boolean;
  onCursorChange: (index: number) => void;
  onSeekTrajectory: (sample: TrajectorySample) => void;
  onStep: (direction: -1 | 1) => void;
  playMode: PlayMode;
  playing: boolean;
  speed: PlaySpeed;
  onPlayModeChange: (mode: PlayMode) => void;
  onPlayingChange: (playing: boolean) => void;
  onSpeedChange: (speed: PlaySpeed) => void;
  selected: boolean;
  /** Optional behavior analysis for class-colored path + POI overlay. */
  behaviorSummary?: BehaviorSummary | null;
  /** Single suspected teleport to draw (from behavior panel click); null = hide. */
  highlightedTeleport?: TeleportSuspect | null;
  /** POI dwell/anchor currently focused from the side list. */
  highlightedPOIId?: string | null;
  /** Pan/zoom target from list clicks; fit once per key. */
  focusTarget?: MapFocusTarget | null;
};

export function TimelineMap({
  activeIndex,
  items,
  loading,
  onCursorChange,
  onSeekTrajectory,
  onStep,
  playMode,
  playing,
  speed,
  onPlayModeChange,
  onPlayingChange,
  onSpeedChange,
  selected,
  behaviorSummary = null,
  highlightedTeleport = null,
  highlightedPOIId = null,
  focusTarget = null,
}: TimelineMapProps) {
  const mapElementRef = useRef<HTMLDivElement>(null);
  const trackRef = useRef<HTMLDivElement>(null);
  const leafletRef = useRef<L.Map | null>(null);
  const clusterGroupRef = useRef<L.MarkerClusterGroup | null>(null);
  const expandedLayerRef = useRef<L.LayerGroup | null>(null);
  const lineLayerRef = useRef<L.LayerGroup | null>(null);
  const behaviorOverlayRef = useRef<L.LayerGroup | null>(null);
  const focusLayerRef = useRef<L.LayerGroup | null>(null);
  const landmarkLayerRef = useRef<L.LayerGroup | null>(null);
  const tileLayerRef = useRef<L.TileLayer | null>(null);
  const markersByKeyRef = useRef(new Map<string, SampleCircleMarker>());
  const expandStateRef = useRef<ExpandState>({ markers: [] });
  const breathTickRef = useRef(0);
  const lastFocusKeyRef = useRef('');
  const activeSampleKeyRef = useRef('');
  const onSeekTrajectoryRef = useRef(onSeekTrajectory);
  onSeekTrajectoryRef.current = onSeekTrajectory;
  const samples = useMemo(() => trajectorySamples(items), [items]);
  const [mapAvailable, setMapAvailable] = useState(true);
  const [colorMode, setColorMode] = useState<'position' | 'ping'>('position');
  const [showPoints, setShowPoints] = useState(true);
  const [showLines, setShowLines] = useState(true);
  const [showStaticLandmarks, setShowStaticLandmarks] = useState(false);
  const [showGuildBases, setShowGuildBases] = useState(false);
  const [guildBases, setGuildBases] = useState<WorldPOI[]>([]);
  const [enabledGuildIDs, setEnabledGuildIDs] = useState<Set<string> | null>(null);
  const [guildFilterOpen, setGuildFilterOpen] = useState(false);
  const [trackWidthPx, setTrackWidthPx] = useState(320);
  const points = useMemo<MapPoint[]>(() => {
    return samples.map((sample) => {
      return { key: trajectoryKey(sample), latLng: projectWorldSample(sample), sample };
    });
  }, [samples]);
  const activeItem = items[activeIndex];
  const activeSample = latestPointAt(samples, activeItem?.at);
  const activeSampleKey = activeSample ? trajectoryKey(activeSample) : '';
  activeSampleKeyRef.current = activeSampleKey;
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
      maxClusterRadius: MAX_CLUSTER_RADIUS,
      disableClusteringAtZoom: DISABLE_CLUSTERING_AT_ZOOM,
      // No spiderfy: we expand clusters by rendering true coordinates instead.
      spiderfyOnMaxZoom: false,
      spiderfyOnEveryZoom: false,
      zoomToBoundsOnClick: true,
      animate: !(typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches),
      iconCreateFunction(cluster) {
        const count = cluster.getChildCount();
        const sizeKey = count < 10 ? 'sm' : count < 50 ? 'md' : 'lg';
        const px = CLUSTER_ICON_PX[sizeKey];
        const activeKey = activeSampleKeyRef.current;
        const containsFocus = !!activeKey && cluster.getAllChildMarkers().some((child) => {
          const key = (child as unknown as SampleCircleMarker).options.sampleKey;
          return key === activeKey;
        });
        return L.divIcon({
          html: '<div class="timeline-marker-cluster-dot" aria-hidden="true"></div>',
          className: `timeline-marker-cluster timeline-marker-cluster--${sizeKey}${containsFocus ? ' is-focus' : ''}`,
          iconSize: L.point(px, px),
          iconAnchor: L.point(px / 2, px / 2),
        });
      },
    });
    // Count only on hover — keep the default chip free of numbers.
    clusterGroup.on('clustermouseover', (event) => {
      const layer = event.layer as L.MarkerCluster;
      const count = layer.getChildCount();
      layer
        .bindTooltip(`${count}`, {
          direction: 'top',
          opacity: 0.95,
          className: 'timeline-cluster-count-tip',
          offset: L.point(0, -4),
        })
        .openTooltip();
    });
    clusterGroup.on('clustermouseout', (event) => {
      const layer = event.layer as L.MarkerCluster;
      layer.closeTooltip();
      layer.unbindTooltip();
    });
    // Exact-coordinate siblings of the focused cluster (no radial spiderfy).
    const expandedLayer = L.layerGroup();
    const lineLayer = L.layerGroup();
    const behaviorOverlay = L.layerGroup();
    const focusLayer = L.layerGroup();
    const landmarkLayer = L.layerGroup();
    clusterGroup.addTo(map);
    expandedLayer.addTo(map);
    landmarkLayer.addTo(map);
    lineLayer.addTo(map);
    behaviorOverlay.addTo(map);
    focusLayer.addTo(map);
    leafletRef.current = map;
    tileLayerRef.current = tileLayer;
    clusterGroupRef.current = clusterGroup;
    expandedLayerRef.current = expandedLayer;
    lineLayerRef.current = lineLayer;
    behaviorOverlayRef.current = behaviorOverlay;
    focusLayerRef.current = focusLayer;
    landmarkLayerRef.current = landmarkLayer;
    const invalidate = () => {
      try {
        map.invalidateSize({ animate: false });
      } catch {
        /* map torn down */
      }
    };
    // Layout can change (toolbar / dock); keep tiles aligned to the container.
    const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(() => invalidate()) : null;
    ro?.observe(mapElementRef.current);
    requestAnimationFrame(invalidate);
    return () => {
      ro?.disconnect();
      map.remove();
      leafletRef.current = null;
      tileLayerRef.current = null;
      clusterGroupRef.current = null;
      expandedLayerRef.current = null;
      lineLayerRef.current = null;
      behaviorOverlayRef.current = null;
      focusLayerRef.current = null;
      landmarkLayerRef.current = null;
      markersByKeyRef.current.clear();
      clearExpandState(expandStateRef.current);
    };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void getGuildBases(controller.signal)
      .then((data) => {
        if (!controller.signal.aborted) setGuildBases(data.pois ?? []);
      })
      .catch(() => {
        if (!controller.signal.aborted) setGuildBases([]);
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const layer = landmarkLayerRef.current;
    if (!layer) return;
    layer.clearLayers();
    if (showStaticLandmarks) {
      drawStaticLandmarks(layer, MAP_LANDMARKS);
    }
    if (showGuildBases) {
      drawGuildBaseLandmarks(layer, filterGuildBases(guildBases, enabledGuildIDs));
    }
  }, [enabledGuildIDs, guildBases, showGuildBases, showStaticLandmarks]);

  useEffect(() => {
    const clusterGroup = clusterGroupRef.current;
    const expandedLayer = expandedLayerRef.current;
    if (!clusterGroup || !expandedLayer) return;
    // Markers are recreated; drop expansion bookkeeping (old layers are gone).
    expandedLayer.clearLayers();
    clearExpandState(expandStateRef.current);
    clusterGroup.clearLayers();
    markersByKeyRef.current.clear();
    if (!showPoints) {
      clusterGroup.refreshClusters();
      return;
    }
    points.forEach((point) => {
      const colors = colorMode === 'ping'
        ? pingColor(point.sample.ping)
        : { fill: '#fffdf7', stroke: '#0f7285', radius: 4 };
      const marker = L.circleMarker(point.latLng, {
        radius: colors.radius,
        color: colors.stroke,
        fillColor: colors.fill,
        fillOpacity: 1,
        weight: 2,
      }) as SampleCircleMarker;
      marker.options.sampleKey = point.key;
      marker.on('click', () => onSeekTrajectoryRef.current(point.sample));
      markersByKeyRef.current.set(point.key, marker);
      clusterGroup.addLayer(marker);
    });
    clusterGroup.refreshClusters();
    const focusMarker = markersByKeyRef.current.get(activeSampleKeyRef.current);
    expandClusterForExactPoints(clusterGroup, expandedLayer, expandStateRef.current, focusMarker);
  }, [colorMode, points, showPoints]);

  useEffect(() => {
    const map = leafletRef.current;
    const clusterGroup = clusterGroupRef.current;
    const expandedLayer = expandedLayerRef.current;
    const lineLayer = lineLayerRef.current;
    const behaviorOverlay = behaviorOverlayRef.current;
    const focusLayer = focusLayerRef.current;
    if (!map || !clusterGroup || !expandedLayer || !lineLayer || !behaviorOverlay || !focusLayer) return;

    // Expand focus cluster into exact-coordinate points (not spiderfy).
    if (showPoints) {
      const focusMarker = markersByKeyRef.current.get(activeSampleKey);
      expandClusterForExactPoints(clusterGroup, expandedLayer, expandStateRef.current, focusMarker);
      clusterGroup.refreshClusters();
    } else {
      expandedLayer.clearLayers();
      clearExpandState(expandStateRef.current);
    }

    lineLayer.clearLayers();
    behaviorOverlay.clearLayers();
    const activeAt = activeItem?.at;
    const lineSamples = hybridTrajectoryWindow(samples, activeAt);
    const split = splitTrajectoryPastFuture(lineSamples, activeAt);
    const projectAll = (list: typeof lineSamples) => list.map((s) => projectWorldSample(s));

    if (showLines) {
      if (split.future.length >= 2) {
        L.polyline(projectAll(split.future), {
          color: TRAJ_FUTURE_COLOR,
          opacity: 0.28,
          weight: TRAJ_FUTURE_WEIGHT + 3,
          lineCap: 'round',
          lineJoin: 'round',
          className: 'timeline-traj-future-glow',
          interactive: false,
        }).addTo(lineLayer);
        L.polyline(projectAll(split.future), {
          color: TRAJ_FUTURE_COLOR,
          opacity: TRAJ_FUTURE_OPACITY,
          weight: TRAJ_FUTURE_WEIGHT,
          lineCap: 'round',
          lineJoin: 'round',
          dashArray: TRAJ_FUTURE_DASH_ARRAY,
          className: 'timeline-traj-future',
        }).addTo(lineLayer);
      }
      // Past path: per-edge behavior class colors (跑图/局部/挂机).
      if (split.past.length >= 2) {
        for (let i = 0; i < split.past.length - 1; i += 1) {
          const a = split.past[i]!;
          const b = split.past[i + 1]!;
          const edgeClass = edgeClassBetween(a, b);
          if (edgeClass === 'gap') continue;
          const color = BEHAVIOR_EDGE_COLORS[edgeClass];
          const weight = edgeClass === 'traveling' ? 5.5 : edgeClass === 'local' ? 4.5 : 3.5;
          L.polyline([projectWorldSample(a), projectWorldSample(b)], {
            color,
            opacity: edgeClass === 'stationary' ? 0.75 : 0.95,
            weight,
            lineCap: 'round',
            lineJoin: 'round',
            className: `timeline-traj-edge timeline-traj-edge--${edgeClass}`,
          }).addTo(lineLayer);
        }
        const tip = split.past.slice(-2);
        L.polyline(projectAll(tip), {
          color: TRAJ_TIP_COLOR,
          opacity: 1,
          weight: TRAJ_TIP_WEIGHT,
          lineCap: 'round',
          lineJoin: 'round',
          className: 'timeline-traj-tip',
        }).addTo(lineLayer);
      }
    }

    // POI dwell pins + teleport arcs from behavior analysis.
    if (behaviorSummary) {
      for (const dwell of behaviorSummary.poiDwells) {
        if (!Number.isFinite(dwell.x) || !Number.isFinite(dwell.y)) continue;
        const kindClass =
          dwell.kind === 'boss_tower' ? 'tower' : dwell.kind === 'guild_base' ? 'guild' : 'ft';
        const active = highlightedPOIId === dwell.poiId;
        const radius = (dwell.kind === 'guild_base' ? 9 : dwell.kind === 'boss_tower' ? 8 : 7) + (active ? 3 : 0);
        const fill =
          dwell.kind === 'boss_tower' ? '#e8b86d' : dwell.kind === 'guild_base' ? '#c4b5fd' : '#9fd0db';
        const stroke =
          dwell.kind === 'boss_tower' ? '#8d5a0f' : dwell.kind === 'guild_base' ? '#6d28d9' : '#0f7285';
        const marker = L.circleMarker(projectWorldXY(dwell.x!, dwell.y!), {
          radius,
          color: active ? '#ca8519' : stroke,
          fillColor: fill,
          fillOpacity: 0.92,
          weight: active ? 3.5 : 2.5,
          className: `timeline-behavior-poi timeline-behavior-poi--${kindClass}${active ? ' is-active' : ''}`,
        });
        marker.bindTooltip(
          `${dwell.nameZh} · 驻留 ${Math.max(1, Math.round(dwell.dwellMs / 60_000))} 分`,
          { direction: 'top', opacity: 0.95 },
        );
        marker.addTo(behaviorOverlay);
        if (active) {
          L.circleMarker(projectWorldXY(dwell.x!, dwell.y!), {
            radius: radius + 8,
            color: '#ca8519',
            fillOpacity: 0,
            weight: 2,
            className: 'timeline-behavior-poi-focus-ring',
            interactive: false,
          }).addTo(behaviorOverlay);
        }
      }
      if (behaviorSummary.activityAnchor && Number.isFinite(behaviorSummary.activityAnchor.x) && Number.isFinite(behaviorSummary.activityAnchor.y)) {
        const a = behaviorSummary.activityAnchor;
        const anchorActive = highlightedPOIId === a.poiId;
        L.circleMarker(projectWorldXY(a.x!, a.y!), {
          radius: anchorActive ? 18 : 14,
          color: '#ca8519',
          fillOpacity: 0,
          weight: anchorActive ? 3 : 2,
          className: 'timeline-behavior-anchor-ring',
          interactive: false,
        }).addTo(behaviorOverlay);
      }
    }

    // Draw one teleport arc only when the user selects a list item.
    if (
      highlightedTeleport
      && Number.isFinite(highlightedTeleport.fromX)
      && Number.isFinite(highlightedTeleport.fromY)
      && Number.isFinite(highlightedTeleport.toX)
      && Number.isFinite(highlightedTeleport.toY)
      && !(highlightedTeleport.fromX === highlightedTeleport.toX && highlightedTeleport.fromY === highlightedTeleport.toY)
    ) {
      const hop = highlightedTeleport;
      const line = L.polyline(
        [projectWorldXY(hop.fromX!, hop.fromY!), projectWorldXY(hop.toX!, hop.toY!)],
        {
          color: '#f59e0b',
          opacity: 0.9,
          weight: 3,
          dashArray: '8 10',
          className: 'timeline-behavior-teleport',
        },
      );
      const fromLabel = hop.fromNameZh ?? '野外';
      const toLabel = hop.toNameZh ?? '野外';
      const reason = hop.reason === 'gap_hop' ? '跨段' : '大跳';
      line.bindTooltip(`疑似传送 · ${reason} · ${fromLabel} → ${toLabel}`, {
        sticky: true,
        opacity: 0.95,
      });
      line.addTo(behaviorOverlay);
    }

    focusLayer.clearLayers();
    const focusSample = latestPointAt(samples, activeAt);
    if (focusSample) {
      const activePoint = projectWorldSample(focusSample);
      const ping = colorMode === 'ping' ? pingColor(focusSample.ping) : null;
      // Bump tick so CSS breath hit restarts even when focus stays in the same cluster.
      breathTickRef.current += 1;
      const breathTick = breathTickRef.current;

      const expandedKeys = expandStateRef.current.markers
        .map((layer) => (layer as SampleCircleMarker).options.sampleKey)
        .filter((key): key is string => typeof key === 'string' && key.length > 0);
      const breathTargets = collectBreathTargets(samples, activeSampleKey, trajectoryKey, expandedKeys);
      // Draw softer roles first; focus ring last under the solid core.
      const roleOrder = { sibling: 0, prev: 1, next: 2, focus: 3 } as const;
      [...breathTargets]
        .sort((a, b) => roleOrder[a.role] - roleOrder[b.role])
        .forEach((target) => {
          const style = BREATH_COLORS[target.role];
          L.circleMarker(projectWorldSample(target.sample), {
            radius: style.radius,
            color: style.stroke,
            fillColor: style.fill,
            fillOpacity: target.role === 'focus' ? 0 : 0.14,
            weight: style.weight,
            className: `timeline-breath timeline-breath--${target.role} timeline-breath-tick-${breathTick % 2}`,
            interactive: false,
          }).addTo(focusLayer);
        });

      L.circleMarker(activePoint, {
        radius: ping ? Math.max(8, ping.radius + 3) : 8,
        color: '#8d5a0f',
        fillColor: ping?.fill ?? '#ca8519',
        fillOpacity: 0.92,
        weight: 3,
        className: 'timeline-focus-core',
      }).addTo(focusLayer);

      // Direction arrows: prev→focus (arrive) preferred; else focus→next (leave).
      // Also place mid-edge chevrons so travel sense is visible around neighbors.
      const prevTarget = breathTargets.find((t) => t.role === 'prev');
      const nextTarget = breathTargets.find((t) => t.role === 'next');
      const prevPoint = prevTarget ? projectWorldSample(prevTarget.sample) : undefined;
      const nextPoint = nextTarget ? projectWorldSample(nextTarget.sample) : undefined;

      const addArrow = (
        at: L.LatLngExpression,
        from: L.LatLngExpression,
        to: L.LatLngExpression,
        tone: 'focus' | 'prev' | 'next',
        size: number,
        nudgePx: number,
      ) => {
        const deg = bearingDeg(from, to);
        if (!Number.isFinite(deg)) return;
        const icon = L.divIcon({
          className: `timeline-traj-arrow timeline-traj-arrow--${tone}`,
          html: `<div class="timeline-traj-arrow-inner" style="transform:rotate(${deg}deg) translateY(-${nudgePx}px)"></div>`,
          iconSize: [size, size],
          iconAnchor: [size / 2, size / 2],
        });
        L.marker(at, { icon, interactive: false, keyboard: false, zIndexOffset: 600 }).addTo(focusLayer);
      };

      const travel = travelBearingEndpoints(prevPoint, activePoint, nextPoint);
      if (travel) {
        addArrow(activePoint, travel.from, travel.to, 'focus', ARROW_SIZE_PX, 12);
      }
      if (prevPoint) {
        addArrow(midpointLatLng(prevPoint, activePoint), prevPoint, activePoint, 'prev', ARROW_EDGE_SIZE_PX, 0);
      }
      if (nextPoint) {
        addArrow(midpointLatLng(activePoint, nextPoint), activePoint, nextPoint, 'next', ARROW_EDGE_SIZE_PX, 0);
      }

      // Force SVG path CSS animations to restart on every cursor step.
      requestAnimationFrame(() => {
        focusLayer.eachLayer((layer) => {
          const el = (layer as L.Path).getElement?.();
          if (!el) return;
          el.classList.remove('timeline-breath-run');
          // Reflow so removing/adding the run class restarts keyframes.
          void (el as HTMLElement).offsetWidth;
          el.classList.add('timeline-breath-run');
        });
      });

      if (shouldPanToFocus(map.getBounds(), activePoint)) {
        map.panTo(activePoint, { animate: false });
      }
    }
  }, [activeItem?.at, activeSampleKey, behaviorSummary, colorMode, highlightedPOIId, highlightedTeleport, samples, showLines, showPoints]);

  // Focus map when the user clicks a POI or teleport in the side list.
  useEffect(() => {
    const map = leafletRef.current;
    if (!map || !focusTarget || focusTarget.points.length === 0) {
      lastFocusKeyRef.current = '';
      return;
    }
    if (lastFocusKeyRef.current === focusTarget.key) return;
    lastFocusKeyRef.current = focusTarget.key;
    const latLngs = focusTarget.points
      .filter((p) => Number.isFinite(p.x) && Number.isFinite(p.y))
      .map((p) => projectWorldXY(p.x, p.y));
    if (latLngs.length === 0) return;
    try {
      if (latLngs.length === 1) {
        const zoom = Math.max(map.getZoom(), 4);
        map.setView(latLngs[0] as L.LatLngExpression, Math.min(zoom, 5), { animate: true });
      } else {
        map.fitBounds(L.latLngBounds(latLngs as L.LatLngExpression[]).pad(0.35), {
          animate: true,
          maxZoom: 4,
        });
      }
    } catch {
      /* ignore empty bounds */
    }
  }, [focusTarget]);

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
        <div className="timeline-layer-toggles" role="group" aria-label="轨迹图层">
          <button
            type="button"
            className="timeline-layer-toggle"
            aria-pressed={showPoints}
            onClick={() => setShowPoints((value) => !value)}
          >
            点
          </button>
          <button
            type="button"
            className="timeline-layer-toggle"
            aria-pressed={showLines}
            onClick={() => setShowLines((value) => !value)}
          >
            线
          </button>
        </div>
        <LandmarkLayerControls
          showStatic={showStaticLandmarks}
          onShowStaticChange={setShowStaticLandmarks}
          showGuildBases={showGuildBases}
          onShowGuildBasesChange={setShowGuildBases}
          guildBases={guildBases}
          enabledGuildIDs={enabledGuildIDs}
          onEnabledGuildIDsChange={setEnabledGuildIDs}
          guildFilterOpen={guildFilterOpen}
          onGuildFilterOpenChange={setGuildFilterOpen}
        />
        <span className="timeline-map-count"><Route size={15} /> {samples.length} 个坐标</span>
      </div>
    </div>
    {colorMode === 'ping' ? (
      <div className="timeline-ping-legend" aria-label="延迟图例">
        <span className="timeline-ping-legend-title">延迟</span>
        {PING_LEGEND.map((entry) => (
          <span className="timeline-ping-legend-item" key={entry.label}>
            <span className="timeline-ping-swatch" style={{ background: entry.fill }} aria-hidden="true" />
            <span className="timeline-ping-glyph" aria-hidden="true">{entry.glyph}</span>
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
          <span className="sr-only">播放模式</span>
          <select
            aria-label="播放模式"
            value={playMode}
            disabled={items.length < 2}
            onChange={(event) => onPlayModeChange(event.target.value as PlayMode)}
          >
            <option value="index">按条</option>
            <option value="time">按时间</option>
          </select>
        </label>
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
