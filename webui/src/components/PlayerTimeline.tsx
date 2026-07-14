import { useEffect, useMemo, useRef, useState } from 'react';
import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import 'leaflet.markercluster';
import 'leaflet.markercluster/dist/MarkerCluster.css';
import { AlertTriangle, Compass, Crosshair, Map as MapIcon, Radio, Route, Search, ShieldAlert } from 'lucide-react';
import { ApiError, getPlayerTimeline, type Player, type PlayerTimelineResponse, type TimelineEvent } from '../api';
import { knownMapLocation as resolveLandmark } from '../map/mapLandmarks';
import { nextCursorIndex, playIntervalMs, PLAY_SPEEDS, prevCursorIndex, type PlaySpeed } from '../map/mapPlayback';
import { hybridTrajectoryWindow, pingColor } from '../map/mapTrajectory';
import { shouldPanToFocus } from '../map/mapView';

type Props = { includePrivate?: boolean; players: Player[]; refreshKey: number };
type TimelineState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: PlayerTimelineResponse }
  | { kind: 'not-found' }
  | { kind: 'error'; message: string };

type LogItem =
  | { kind: 'event'; at: string; key: string; item: TimelineEvent }
  | { kind: 'trajectory'; at: string; key: string; item: PlayerTimelineResponse['trajectories'][number] }
  | { kind: 'private'; at: string; key: string; item: PlayerTimelineResponse['private_samples'][number] };
type LogRow = { item: LogItem; separator: string };
type TrajectorySample = PlayerTimelineResponse['trajectories'][number];
type MapPoint = { key: string; sample: TrajectorySample; latLng: L.LatLngExpression };

const MAX_RANGE_MS = 31 * 24 * 60 * 60 * 1000;
// Same Leaflet CRS.Simple coordinate transform used by zaigie/palworld-server-tool.
// LAND_SCAPE order in the reference repo is [maxX, maxY, minX, minY].
const PALWORLD_LANDSCAPE = [349400, 724400, -1099400, -724400] as const;
// Real Palworld map tiles are vendored into webui/public/map/tiles via Git LFS
// (mirrored from https://palworld.gg/images/tiles). The map prefers the local
// copy and transparently falls back to palworld.gg per tile when it is missing,
// so deployments without resolved LFS objects still render the full map.
const PALWORLD_TILE_URL = '/map/tiles/{z}/{x}/{y}.png';
const PALWORLD_TILE_FALLBACK_URL = 'https://palworld.gg/images/tiles/{z}/{x}/{y}.png';
const PALWORLD_TILE_BOUNDS: L.LatLngBoundsExpression = [[0, 0], [-256, 256]];
const KNOWN_EVENTS = new Set([
  'player_joined', 'player_left', 'player_attribute_changed',
  'guard_warning_attempted', 'guard_warning_delivered', 'guard_warning_failed',
  'enforcement_attempted', 'enforcement_succeeded', 'enforcement_failed',
]);
const EVENT_LABELS: Record<string, string> = {
  player_joined: '玩家加入',
  player_left: '玩家离开',
  player_attribute_changed: '玩家属性变更',
  guard_warning_attempted: '尝试发送提醒',
  guard_warning_delivered: '提醒已送达',
  guard_warning_failed: '提醒发送失败',
  enforcement_attempted: '尝试执行限制',
  enforcement_succeeded: '限制执行成功',
  enforcement_failed: '限制执行失败',
};
const CONFIDENCE_LABELS: Record<string, string> = {
  observed: '实测',
  snapshot_derived: '存档推导',
};
const LOCAL_DATE_TIME = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;

// Pure fallback decision for a failed tile: retry the same coords against
// palworld.gg on the first error, then give up (marking the layer unavailable)
// once the remote copy also fails. Kept separate from Leaflet so it is testable.
export function tileErrorTransition(alreadyFellBack: boolean, coords: { x: number; y: number; z: number }): { action: 'retry'; src: string } | { action: 'fail' } {
  if (alreadyFellBack) return { action: 'fail' };
  return { action: 'retry', src: L.Util.template(PALWORLD_TILE_FALLBACK_URL, coords) };
}

// Tile layer that serves the vendored local tiles first and retries the same
// tile against palworld.gg when the local object is missing (e.g. Git LFS not
// pulled). Only the second failure marks the whole layer as unavailable.
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


function localInputValue(date: Date) {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

function defaultRange() {
  const end = new Date();
  return { start: localInputValue(new Date(end.getTime() - 24 * 60 * 60 * 1000)), end: localInputValue(end) };
}

export function parseLocalDateTime(value: string): { date?: Date; error?: string } {
  const match = LOCAL_DATE_TIME.exec(value);
  if (!match) return { error: '请输入有效的本地日期和时间。' };
  const [, yearText, monthText, dayText, hourText, minuteText] = match;
  const parts = [yearText, monthText, dayText, hourText, minuteText].map(Number);
  const [year, month, day, hour, minute] = parts;
  if (year < 1000 || month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59) return { error: '请输入有效的本地日期和时间。' };
  const date = new Date(year, month - 1, day, hour, minute, 0, 0);
  const matchesWallTime = (candidate: Date) => candidate.getFullYear() === year && candidate.getMonth() === month - 1 && candidate.getDate() === day && candidate.getHours() === hour && candidate.getMinutes() === minute;
  if (!matchesWallTime(date)) return { error: '该本地时间因时钟切换不存在。' };
  for (let offsetMinutes = -180; offsetMinutes <= 180; offsetMinutes += 1) {
    if (offsetMinutes === 0) continue;
    const candidate = new Date(date.getTime() + offsetMinutes * 60_000);
    if (matchesWallTime(candidate)) return { error: '该本地时间因时钟回拨存在歧义，请选择其他时间。' };
  }
  return { date };
}

function validateRange(start: string, end: string) {
  const parsedStart = parseLocalDateTime(start);
  const parsedEnd = parseLocalDateTime(end);
  if (parsedStart.error) return `开始时间：${parsedStart.error}`;
  if (parsedEnd.error) return `结束时间：${parsedEnd.error}`;
  const startMS = parsedStart.date!.getTime();
  const endMS = parsedEnd.date!.getTime();
  if (endMS <= startMS) return '结束时间必须晚于开始时间。';
  if (endMS - startMS > MAX_RANGE_MS) return '选择的时间范围不能超过 31 天。';
  return '';
}

function mergeTimeline(data: PlayerTimelineResponse, includePrivate: boolean): LogItem[] {
  const merged: LogItem[] = [];
  data.events.forEach((item) => merged.push({ kind: 'event', at: item.occurred_at, key: `event:${item.id}`, item }));
  data.trajectories.forEach((item) => merged.push({ kind: 'trajectory', at: item.observed_at, key: `trajectory:${trajectoryKey(item)}`, item }));
  if (includePrivate) data.private_samples.forEach((item) => merged.push({ kind: 'private', at: item.observed_at, key: `private:${item.user_id}:${item.observed_at}:${item.source_ref}`, item }));
  return merged.sort((a, b) => Date.parse(a.at) - Date.parse(b.at) || a.key.localeCompare(b.key));
}

function trajectoryKey(sample: TrajectorySample) {
  return `${sample.user_id}:${sample.segment_id}:${sample.observed_at}:${sample.source_ref}`;
}

function annotateTrajectoryEvidence(items: LogItem[]): LogRow[] {
  let previousTrajectory: PlayerTimelineResponse['trajectories'][number] | undefined;
  return items.map((item) => {
    let separator = '';
    if (item.kind === 'trajectory') {
      if (previousTrajectory && item.item.segment_id !== previousTrajectory.segment_id) {
        separator = '新轨迹段：中间路径未推断';
      }
      previousTrajectory = item.item;
    }
    return { item, separator };
  });
}

function trajectorySamples(items: LogItem[]) {
  return items.filter((item): item is Extract<LogItem, { kind: 'trajectory' }> => item.kind === 'trajectory').map((item) => item.item);
}

function projectWorldSample(sample: TrajectorySample): L.LatLngExpression {
  const [maxX, maxY, minX, minY] = PALWORLD_LANDSCAPE;
  if (sample.x >= -256 && sample.x <= 256) return [sample.x, sample.y];
  const x = -256 + (256 * (sample.x - minX)) / (maxX - minX);
  const y = (256 * (sample.y - minY)) / (maxY - minY);
  return [x, y];
}

function latestPointAt(samples: TrajectorySample[], activeAt: string | undefined) {
  if (!samples.length) return undefined;
  const activeMS = activeAt ? Date.parse(activeAt) : Number.NaN;
  if (!Number.isFinite(activeMS)) return samples[0];
  return [...samples].reverse().find((sample) => Date.parse(sample.observed_at) <= activeMS) ?? samples[0];
}

function timelinePercent(at: string, startMS: number, endMS: number) {
  const current = Date.parse(at);
  if (!Number.isFinite(current) || endMS <= startMS) return 0;
  return Math.min(100, Math.max(0, ((current - startMS) / (endMS - startMS)) * 100));
}

function formatTimelineDateTime(value: string | undefined): string {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date);
}

function eventLabel(eventType: string, known = KNOWN_EVENTS.has(eventType)) {
  return known ? EVENT_LABELS[eventType] ?? eventType : '未知事件';
}

function confidenceLabel(value: string) {
  return CONFIDENCE_LABELS[value] ?? value;
}

function segmentLabels(items: LogItem[]) {
  const labels = new Map<string, string>();
  trajectorySamples(items).forEach((sample) => {
    const key = sample.segment_id || '';
    if (key && !labels.has(key)) labels.set(key, `第 ${labels.size + 1} 段`);
  });
  return labels;
}

function trajectoryLocationLabels(items: LogItem[]) {
  const samples = trajectorySamples(items);
  const labels = new Map<string, string>();
  samples.forEach((sample) => labels.set(trajectoryKey(sample), mapLocationLabel(sample)));
  return labels;
}

function mapLocationLabel(sample: TrajectorySample) {
  const landmark = knownMapLocation(sample);
  return landmark || '地图坐标位置';
}

function knownMapLocation(sample: TrajectorySample): string {
  return resolveLandmark(sample);
}

const PING_LEGEND: Array<{ label: string; fill: string }> = [
  { label: '<50', fill: pingColor(20).fill },
  { label: '50–80', fill: pingColor(60).fill },
  { label: '80–120', fill: pingColor(100).fill },
  { label: '120–200', fill: pingColor(150).fill },
  { label: '>200', fill: pingColor(250).fill },
  { label: '未知', fill: pingColor(Number.NaN).fill },
];

export function PlayerTimeline({ includePrivate = false, players, refreshKey }: Props) {
  const range = useMemo(defaultRange, []);
  const [selectedID, setSelectedID] = useState('');
  const [search, setSearch] = useState('');
  const [start, setStart] = useState(range.start);
  const [end, setEnd] = useState(range.end);
  const [state, setState] = useState<TimelineState>({ kind: 'idle' });
  const [cursorIndex, setCursorIndex] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState<PlaySpeed>(1);
  const requestID = useRef(0);
  const lastRefreshKey = useRef(refreshKey);
  const rangeError = validateRange(start, end);
  const visiblePlayers = useMemo(() => {
    const term = search.trim().toLowerCase();
    if (!term) return players;
    return players.filter((player) => player.user_id === selectedID || [player.name, player.account_name, player.user_id, player.player_id].some((value) => value.toLowerCase().includes(term)));
  }, [players, search, selectedID]);

  function applyPresetHours(hours: number) {
    const nextEnd = new Date();
    setEnd(localInputValue(nextEnd));
    setStart(localInputValue(new Date(nextEnd.getTime() - hours * 3600_000)));
  }

  function seekCursor(index: number) {
    setPlaying(false);
    setCursorIndex(index);
  }

  useEffect(() => {
    if (lastRefreshKey.current !== refreshKey) {
      lastRefreshKey.current = refreshKey;
      const nextEnd = localInputValue(new Date());
      if (nextEnd !== end) {
        setEnd(nextEnd);
        return;
      }
    }
    const id = ++requestID.current;
    if (!selectedID) {
      setState({ kind: 'idle' });
      return;
    }
    if (rangeError) {
      setState({ kind: 'error', message: rangeError });
      return;
    }
    const controller = new AbortController();
    setState({ kind: 'loading' });
    const parsedStart = parseLocalDateTime(start).date!;
    const parsedEnd = parseLocalDateTime(end).date!;
    void getPlayerTimeline(selectedID, parsedStart.toISOString(), parsedEnd.toISOString(), 500, controller.signal, includePrivate)
      .then((data) => {
        if (!controller.signal.aborted && requestID.current === id) setState({ kind: 'ready', data });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted || requestID.current !== id) return;
        if (error instanceof ApiError && error.status === 404) setState({ kind: 'not-found' });
        else setState({ kind: 'error', message: error instanceof Error ? error.message : 'Timeline request failed.' });
      });
    return () => controller.abort();
  }, [includePrivate, selectedID, start, end, refreshKey, rangeError]);

  const items = useMemo(() => state.kind === 'ready' ? mergeTimeline(state.data, includePrivate) : [], [includePrivate, state]);
  const rows = useMemo(() => annotateTrajectoryEvidence(items), [items]);
  const segmentNames = useMemo(() => segmentLabels(items), [items]);
  const locationNames = useMemo(() => trajectoryLocationLabels(items), [items]);
  const activeIndex = items.length ? Math.min(cursorIndex, items.length - 1) : 0;
  const mayBeTruncated = state.kind === 'ready' && [state.data.events, state.data.trajectories, includePrivate ? state.data.private_samples : []].some((source) => source.length >= 500);

  function seekTrajectorySample(sample: TrajectorySample) {
    const key = trajectoryKey(sample);
    const index = items.findIndex((item) => item.kind === 'trajectory' && trajectoryKey(item.item) === key);
    if (index >= 0) seekCursor(index);
  }

  useEffect(() => {
    setCursorIndex(0);
    setPlaying(false);
  }, [selectedID, start, end, refreshKey]);

  useEffect(() => {
    if (!playing || items.length < 2) return;
    const id = window.setInterval(() => {
      setCursorIndex((current) => {
        const { index, done } = nextCursorIndex(current, items.length);
        if (done) {
          // schedule outside pure updater
          queueMicrotask(() => setPlaying(false));
        }
        return index;
      });
    }, playIntervalMs(speed));
    return () => window.clearInterval(id);
  }, [playing, speed, items.length]);

  function stepCursor(direction: -1 | 1) {
    if (items.length < 2) return;
    setPlaying(false);
    setCursorIndex((current) => {
      const result = direction === 1
        ? nextCursorIndex(current, items.length)
        : prevCursorIndex(current, items.length);
      return result.index;
    });
  }

  function onTimelineKeyDown(event: React.KeyboardEvent<HTMLElement>) {
    const target = event.target as HTMLElement | null;
    const tag = target?.tagName;
    if (tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || target?.isContentEditable) return;
    if (event.key === ' ' || event.code === 'Space') {
      event.preventDefault();
      if (items.length >= 2) setPlaying((value) => !value);
      return;
    }
    if (event.key === 'ArrowRight') {
      event.preventDefault();
      stepCursor(1);
      return;
    }
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      stepCursor(-1);
    }
  }

  return (
    <section className="timeline-recorder" aria-labelledby="timeline-heading" onKeyDown={onTimelineKeyDown}>
      <header className="timeline-heading">
        <div>
          <p className="eyebrow">{includePrivate ? '管理员观察记录' : '公开观察记录'}</p>
          <h2 id="timeline-heading">玩家观察时间轴</h2>
          <p>仅展示已记录的证据，不自动补全缺口。</p>
        </div>
        {includePrivate ? <span className="timeline-private"><ShieldAlert size={16} /> 管理员私有视图</span> : null}
      </header>
      <div className="timeline-layout">
        <aside className="timeline-filters" aria-label="时间轴筛选">
          <label className="timeline-field">
            <span>搜索已知玩家</span>
            <span className="timeline-input-with-icon"><Search size={16} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="名称、账号或 ID" /></span>
          </label>
          <label className="timeline-field">
            <span>玩家</span>
            <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
              <option value="">请选择玩家…</option>
              {visiblePlayers.map((player) => <option value={player.user_id} key={player.user_id}>{player.name || player.account_name || player.user_id} · {player.account_name || player.user_id}</option>)}
            </select>
          </label>
          <div className="timeline-presets" role="group" aria-label="时间范围预设">
            <button type="button" onClick={() => applyPresetHours(1)}>最近 1 小时</button>
            <button type="button" onClick={() => applyPresetHours(24)}>最近 24 小时</button>
            <button type="button" onClick={() => applyPresetHours(7 * 24)}>最近 7 天</button>
          </div>
          <label className="timeline-field"><span>开始</span><input type="datetime-local" value={start} onChange={(event) => setStart(event.target.value)} /></label>
          <label className="timeline-field"><span>结束</span><input type="datetime-local" value={end} onChange={(event) => setEnd(event.target.value)} /></label>
          <p className="timeline-range-note">本地时间 · 最长 31 天 · 每类最多 500 条</p>
        </aside>
        <div className="timeline-log">
          <TimelineMap
            activeIndex={activeIndex}
            items={items}
            loading={state.kind === 'loading'}
            onCursorChange={seekCursor}
            onSeekTrajectory={seekTrajectorySample}
            onStep={stepCursor}
            playing={playing}
            speed={speed}
            onPlayingChange={setPlaying}
            onSpeedChange={setSpeed}
            selected={Boolean(selectedID)}
          />
          {!selectedID ? <EmptyState icon={<Compass size={28} />} text="选择玩家后查看轨迹和事件。" /> : null}
          {state.kind === 'loading' ? <div className="timeline-skeleton" role="status" aria-label="正在加载时间轴"><span /><span /><span /></div> : null}
          {state.kind === 'not-found' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> 该玩家已不在观察记录中。</div> : null}
          {state.kind === 'error' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> {state.message}</div> : null}
          {state.kind === 'ready' && items.length === 0 ? <EmptyState icon={<Radio size={28} />} text="当前时间范围没有观察记录。" /> : null}
          {mayBeTruncated ? <div className="timeline-alert timeline-alert--info" role="status"><AlertTriangle size={18} /> 某类数据达到 500 条响应上限，结果可能已截断。</div> : null}
          {state.kind === 'ready' && items.length > 0 ? <ol className="timeline-spine" aria-label="按时间排序的观察记录">
            {rows.map(({ item, separator }, index) => (
              <TimelineEntry
                active={index === activeIndex}
                // While autoplay is running the map is the primary surface; do not
                // scroll the page down to each list row (that steals the map).
                followActive={!playing}
                index={index}
                key={item.key}
                item={item}
                locationLabel={item.kind === 'trajectory' ? locationNames.get(trajectoryKey(item.item)) : undefined}
                onSelect={seekCursor}
                segmentLabel={item.kind === 'trajectory' ? segmentNames.get(item.item.segment_id) : undefined}
                separator={separator}
              />
            ))}
          </ol> : null}
        </div>
      </div>
    </section>
  );
}

function TimelineMap({
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
}: {
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
}) {
  const mapElementRef = useRef<HTMLDivElement>(null);
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

  // O1: rebuild cluster markers only when the sample set or color mode changes.
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

  // O1: cursor changes only update polyline, focus marker, and which sample is excluded from the cluster.
  // O7: pan only when the focus leaves a slightly inset viewport.
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
      <div className="timeline-cursor-track" aria-hidden="true">
        {items.map((item, index) => <span className={`timeline-cursor-tick timeline-cursor-tick--${item.kind}`} key={item.key} style={{ left: `${timelinePercent(item.at, startMS, endMS)}%` }} data-active={index === activeIndex ? 'true' : undefined} />)}
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

function TimelineEntry({
  active,
  followActive = true,
  index,
  item,
  locationLabel,
  onSelect,
  segmentLabel,
  separator,
}: {
  active: boolean;
  followActive?: boolean;
  index: number;
  item: LogItem;
  locationLabel?: string;
  onSelect: (index: number) => void;
  segmentLabel?: string;
  separator: string;
}) {
  const rowRef = useRef<HTMLLIElement>(null);
  useEffect(() => {
    if (!active || !followActive) return;
    const node = rowRef.current;
    if (node && typeof node.scrollIntoView === 'function') {
      node.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }
  }, [active, followActive]);
  return <>
    {separator ? <li className="timeline-separator" role="separator"><span>{separator}</span></li> : null}
    <li ref={rowRef} className={`timeline-entry timeline-entry--${item.kind} ${active ? 'is-active' : ''}`}>
      <time dateTime={item.at}>{formatTimelineDateTime(item.at)}</time>
      {item.kind === 'event' ? <EventDetail event={item.item} /> : null}
      {item.kind === 'trajectory' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>位置采样</strong><SourceBadge source="palworld_rest" /></div><dl className="telemetry"><div><dt>地图位置</dt><dd>{locationLabel ?? '地图坐标位置'}</dd></div><div><dt>坐标</dt><dd>{item.item.x}, {item.item.y}</dd></div><div><dt>轨迹段</dt><dd>{segmentLabel ?? '未分段'}</dd></div><div><dt>延迟</dt><dd>{item.item.ping} ms</dd></div><div><dt>等级</dt><dd>{item.item.level}</dd></div></dl></div> : null}
      {item.kind === 'private' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>私有玩家采样</strong><SourceBadge source="palworld_rest" /></div><dl className="telemetry"><div><dt>IP · 管理员私有</dt><dd>{item.item.ip || '不可用'}</dd></div><div><dt>延迟</dt><dd>{item.item.ping} ms</dd></div><div><dt>等级</dt><dd>{item.item.level}</dd></div></dl></div> : null}
      <button aria-label={`将回放光标移动到第 ${index + 1} 条记录`} className="timeline-focus" title="将回放光标移动到这里" type="button" onClick={() => onSelect(index)}><Crosshair size={16} /></button>
    </li>
  </>;
}

function EventDetail({ event }: { event: TimelineEvent }) {
  const known = KNOWN_EVENTS.has(event.event_type) && event.summary !== 'unsupported event payload';
  const occurredDiffers = event.observed_at !== event.occurred_at;
  return <div className="timeline-detail">
    <div className="timeline-title-row"><strong>{eventLabel(event.event_type, known)}</strong><SourceBadge source={event.source} /><span className="confidence-badge">{confidenceLabel(event.confidence)}</span></div>
    {occurredDiffers ? <dl className="telemetry timeline-times"><div><dt>发生时间</dt><dd>{formatTimelineDateTime(event.occurred_at)}</dd></div><div><dt>观测时间</dt><dd>{formatTimelineDateTime(event.observed_at)}</dd></div></dl> : null}
  </div>;
}

function SourceBadge({ source }: { source: string }) {
  const normalized = source.toLowerCase();
  const kind = normalized.includes('snapshot') ? 'snapshot' : normalized.includes('rest') ? 'rest' : 'guard';
  const label = kind === 'snapshot' ? '存档' : kind === 'rest' ? 'REST' : '守护器';
  return <span className={`source-badge source-badge--${kind}`}>{label}</span>;
}

function EmptyState({ icon, text }: { icon: React.ReactNode; text: string }) {
  return <div className="timeline-empty">{icon}<p>{text}</p></div>;
}
