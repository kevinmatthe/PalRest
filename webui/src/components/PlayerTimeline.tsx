import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Compass, Crosshair, Map as MapIcon, Radio, Route, Search, ShieldAlert } from 'lucide-react';
import { ApiError, getPlayerTimeline, type Player, type PlayerTimelineResponse, type TimelineEvent } from '../api';

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
type MapPoint = { key: string; sample: TrajectorySample; x: number; y: number };
type MapBounds = { minX: number; maxX: number; minY: number; maxY: number };
type MapRegion = { id: string; name: string; x: number; y: number; rx: number; ry: number; tone: string };

const MAX_RANGE_MS = 31 * 24 * 60 * 60 * 1000;
const PALWORLD_BOUNDS: MapBounds = { minX: -500000, maxX: 500000, minY: -500000, maxY: 500000 };
const PALWORLD_REGIONS: MapRegion[] = [
  { id: 'astral', name: '雪山地带', x: 34, y: 14, rx: 16, ry: 11, tone: 'snow' },
  { id: 'desert', name: '沙漠地带', x: 71, y: 22, rx: 15, ry: 12, tone: 'sand' },
  { id: 'sakrajima', name: '樱岛', x: 82, y: 49, rx: 10, ry: 8, tone: 'bloom' },
  { id: 'forest', name: '森林与竹林', x: 49, y: 42, rx: 21, ry: 16, tone: 'forest' },
  { id: 'start', name: '初始台地', x: 41, y: 66, rx: 18, ry: 14, tone: 'grass' },
  { id: 'archipelago', name: '海风群岛', x: 66, y: 69, rx: 14, ry: 11, tone: 'coast' },
  { id: 'volcano', name: '火山地带', x: 24, y: 75, rx: 17, ry: 13, tone: 'lava' },
];
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

function trajectoryBounds(samples: TrajectorySample[]): MapBounds | null {
  if (!samples.length) return null;
  const xs = samples.map((sample) => sample.x);
  const ys = samples.map((sample) => sample.y);
  let minX = Math.min(...xs);
  let maxX = Math.max(...xs);
  let minY = Math.min(...ys);
  let maxY = Math.max(...ys);
  const padX = Math.max((maxX - minX) * 0.12, 100);
  const padY = Math.max((maxY - minY) * 0.12, 100);
  minX -= padX;
  maxX += padX;
  minY -= padY;
  maxY += padY;
  if (minX === maxX) {
    minX -= 1;
    maxX += 1;
  }
  if (minY === maxY) {
    minY -= 1;
    maxY += 1;
  }
  return { minX, maxX, minY, maxY };
}

function projectSample(sample: TrajectorySample, bounds: MapBounds): { x: number; y: number } {
  const x = ((sample.x - bounds.minX) / (bounds.maxX - bounds.minX)) * 100;
  const y = 64 - ((sample.y - bounds.minY) / (bounds.maxY - bounds.minY)) * 64;
  return { x: Number(x.toFixed(2)), y: Number(y.toFixed(2)) };
}

function projectWorldSample(sample: TrajectorySample): { x: number; y: number } {
  return projectSample(sample, PALWORLD_BOUNDS);
}

function splitRouteSegments(points: MapPoint[]) {
  return points.reduce<MapPoint[][]>((segments, point) => {
    const current = segments.at(-1);
    if (!current || current.at(-1)?.sample.segment_id !== point.sample.segment_id) segments.push([point]);
    else current.push(point);
    return segments;
  }, []);
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
  const area = nearestMapRegion(sample);
  return landmark ? `${landmark} · ${area}` : area;
}

function knownMapLocation(_sample: TrajectorySample): string {
  // This hook is intentionally centralized so licensed map assets or a location
  // translation table can replace coordinate-only labels without touching UI code.
  return '';
}

function nearestMapRegion(sample: TrajectorySample) {
  const point = projectWorldSample(sample);
  let best = PALWORLD_REGIONS[0];
  let bestScore = Number.POSITIVE_INFINITY;
  for (const region of PALWORLD_REGIONS) {
    const dx = (point.x - region.x) / region.rx;
    const dy = (point.y - region.y) / region.ry;
    const score = dx * dx + dy * dy;
    if (score < bestScore) {
      best = region;
      bestScore = score;
    }
  }
  return bestScore <= 1.35 ? best.name : `${best.name}附近`;
}

export function PlayerTimeline({ includePrivate = false, players, refreshKey }: Props) {
  const range = useMemo(defaultRange, []);
  const [selectedID, setSelectedID] = useState('');
  const [search, setSearch] = useState('');
  const [start, setStart] = useState(range.start);
  const [end, setEnd] = useState(range.end);
  const [state, setState] = useState<TimelineState>({ kind: 'idle' });
  const [cursorIndex, setCursorIndex] = useState(0);
  const requestID = useRef(0);
  const lastRefreshKey = useRef(refreshKey);
  const rangeError = validateRange(start, end);
  const visiblePlayers = useMemo(() => {
    const term = search.trim().toLowerCase();
    if (!term) return players;
    return players.filter((player) => player.user_id === selectedID || [player.name, player.account_name, player.user_id, player.player_id].some((value) => value.toLowerCase().includes(term)));
  }, [players, search, selectedID]);

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

  useEffect(() => {
    setCursorIndex(0);
  }, [selectedID, start, end, refreshKey]);

  return (
    <section className="timeline-recorder" aria-labelledby="timeline-heading">
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
          <label className="timeline-field"><span>开始</span><input type="datetime-local" value={start} onChange={(event) => setStart(event.target.value)} /></label>
          <label className="timeline-field"><span>结束</span><input type="datetime-local" value={end} onChange={(event) => setEnd(event.target.value)} /></label>
          <p className="timeline-range-note">本地时间 · 最长 31 天 · 每类最多 500 条</p>
        </aside>
        <div className="timeline-log">
          <TimelineMap
            activeIndex={activeIndex}
            items={items}
            loading={state.kind === 'loading'}
            onCursorChange={setCursorIndex}
            selected={Boolean(selectedID)}
          />
          {!selectedID ? <EmptyState icon={<Compass size={28} />} text="选择玩家后查看轨迹和事件。" /> : null}
          {state.kind === 'loading' ? <div className="timeline-skeleton" role="status" aria-label="正在加载时间轴"><span /><span /><span /></div> : null}
          {state.kind === 'not-found' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> 该玩家已不在观察记录中。</div> : null}
          {state.kind === 'error' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> {state.message}</div> : null}
          {state.kind === 'ready' && items.length === 0 ? <EmptyState icon={<Radio size={28} />} text="当前时间范围没有观察记录。" /> : null}
          {mayBeTruncated ? <div className="timeline-alert timeline-alert--info" role="status"><AlertTriangle size={18} /> 某类数据达到 500 条响应上限，结果可能已截断。</div> : null}
          {state.kind === 'ready' && items.length > 0 ? <ol className="timeline-spine" aria-label="按时间排序的观察记录">
            {rows.map(({ item, separator }, index) => <TimelineEntry active={index === activeIndex} index={index} key={item.key} item={item} locationLabel={item.kind === 'trajectory' ? locationNames.get(trajectoryKey(item.item)) : undefined} onSelect={setCursorIndex} segmentLabel={item.kind === 'trajectory' ? segmentNames.get(item.item.segment_id) : undefined} separator={separator} />)}
          </ol> : null}
        </div>
      </div>
    </section>
  );
}

function TimelineMap({ activeIndex, items, loading, onCursorChange, selected }: { activeIndex: number; items: LogItem[]; loading: boolean; onCursorChange: (index: number) => void; selected: boolean }) {
  const samples = useMemo(() => trajectorySamples(items), [items]);
  const points = useMemo<MapPoint[]>(() => {
    return samples.map((sample) => {
      const position = projectWorldSample(sample);
      return { ...position, key: `${sample.segment_id}:${sample.observed_at}:${sample.source_ref}`, sample };
    });
  }, [samples]);
  const segments = useMemo(() => splitRouteSegments(points), [points]);
  const activeItem = items[activeIndex];
  const activeSample = latestPointAt(samples, activeItem?.at);
  const activePoint = activeSample ? projectWorldSample(activeSample) : undefined;
  const startMS = items.length ? Math.min(...items.map((item) => Date.parse(item.at))) : 0;
  const endMS = items.length ? Math.max(...items.map((item) => Date.parse(item.at))) : 1;
  const cursorLeft = activeItem ? timelinePercent(activeItem.at, startMS, endMS) : 0;
  const activeLabel = activeItem ? activeItem.kind === 'event' ? `光标：${eventLabel(activeItem.item.event_type)}` : activeItem.kind === 'trajectory' ? '光标：位置采样' : '光标：私有玩家采样' : selected ? '等待观察记录' : '未选择玩家';

  return <section className="timeline-map-panel" aria-label="地图回放">
    <div className="timeline-map-header">
      <div>
        <p className="eyebrow">地图回放</p>
        <h3>{activeLabel}</h3>
      </div>
      <span className="timeline-map-count"><Route size={15} /> {samples.length} 个坐标</span>
    </div>
    <div className="timeline-map-surface">
      <svg aria-label="完整地图" className="timeline-world-map" data-testid="timeline-map" preserveAspectRatio="xMidYMid meet" role="img" viewBox="0 0 100 100">
        <defs>
          <pattern height="5" id="timeline-grid" patternUnits="userSpaceOnUse" width="5">
            <path d="M 8 0 L 0 0 0 8" fill="none" stroke="currentColor" strokeWidth="0.18" />
          </pattern>
          <radialGradient id="timeline-sea" cx="48%" cy="44%" r="65%">
            <stop offset="0%" stopColor="#c9e7ee" />
            <stop offset="100%" stopColor="#76aebb" />
          </radialGradient>
        </defs>
        <rect className="timeline-map-sea" height="100" width="100" />
        <rect className="timeline-map-grid" height="100" width="100" />
        {PALWORLD_REGIONS.map((region) => <g className={`timeline-map-region timeline-map-region--${region.tone}`} key={region.id}>
          <ellipse cx={region.x} cy={region.y} rx={region.rx} ry={region.ry} />
          <text x={region.x} y={region.y}>{region.name}</text>
        </g>)}
        <path className="timeline-map-shore" d="M20 13 C31 4 49 8 59 18 C69 15 82 19 86 33 C98 41 94 59 82 63 C79 79 64 89 48 84 C35 94 17 86 15 70 C4 61 5 45 16 39 C11 29 13 19 20 13 Z" />
        {segments.map((segment) => <polyline className="timeline-map-route" data-testid="timeline-map-route" key={segment[0].key} points={segment.map((point) => `${point.x},${point.y}`).join(' ')} />)}
        {points.map((point) => <circle className="timeline-map-point" cx={point.x} cy={point.y} key={point.key} r="1.35" />)}
        {activePoint ? <g className="timeline-map-active" data-testid="timeline-map-active">
          <circle cx={activePoint.x} cy={activePoint.y} r="3.2" />
          <circle cx={activePoint.x} cy={activePoint.y} r="1.2" />
        </g> : null}
      </svg>
      {!points.length ? <div className="timeline-map-empty">{loading ? '正在加载轨迹证据。' : selected ? '当前时间范围没有位置样本，已显示完整地图。' : '选择玩家后叠加轨迹。'}</div> : null}
    </div>
    <div className="timeline-cursor">
      <div className="timeline-cursor-track" aria-hidden="true">
        {items.map((item, index) => <span className={`timeline-cursor-tick timeline-cursor-tick--${item.kind}`} key={item.key} style={{ left: `${timelinePercent(item.at, startMS, endMS)}%` }} data-active={index === activeIndex ? 'true' : undefined} />)}
        <span className="timeline-cursor-now" style={{ left: `${cursorLeft}%` }} />
      </div>
      <input aria-label="时间轴光标" disabled={items.length < 2} max={Math.max(items.length - 1, 0)} min={0} onChange={(event) => onCursorChange(Number(event.target.value))} type="range" value={activeIndex} />
      <div className="timeline-map-meta">
        <span><MapIcon size={15} /> {activeSample ? mapLocationLabel(activeSample) : '无地图位置'}</span>
        <span>{activeItem ? formatTimelineDateTime(activeItem.at) : '无光标时间'}</span>
      </div>
    </div>
  </section>;
}

function TimelineEntry({ active, index, item, locationLabel, onSelect, segmentLabel, separator }: { active: boolean; index: number; item: LogItem; locationLabel?: string; onSelect: (index: number) => void; segmentLabel?: string; separator: string }) {
  return <>
    {separator ? <li className="timeline-separator" role="separator"><span>{separator}</span></li> : null}
    <li className={`timeline-entry timeline-entry--${item.kind} ${active ? 'is-active' : ''}`}>
      <time dateTime={item.at}>{formatTimelineDateTime(item.at)}</time>
      {item.kind === 'event' ? <EventDetail event={item.item} /> : null}
      {item.kind === 'trajectory' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>位置采样</strong><SourceBadge source="palworld_rest" /></div><dl className="telemetry"><div><dt>地图位置</dt><dd>{locationLabel ?? '未匹配地名 · 坐标位置'}</dd></div><div><dt>坐标</dt><dd>{item.item.x}, {item.item.y}</dd></div><div><dt>轨迹段</dt><dd>{segmentLabel ?? '未分段'}</dd></div><div><dt>延迟</dt><dd>{item.item.ping} ms</dd></div><div><dt>等级</dt><dd>{item.item.level}</dd></div></dl></div> : null}
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
