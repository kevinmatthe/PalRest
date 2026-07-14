import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Compass, Crosshair, Radio, Search, ShieldAlert } from 'lucide-react';
import { ApiError, getPlayerTimeline, type Player, type PlayerTimelineResponse, type TimelineEvent } from '../api';
import { nextCursorIndex, playStepDelayMs, prevCursorIndex, type PlayMode, type PlaySpeed } from '../map/mapPlayback';
import { DEFAULT_ROW_ESTIMATE_PX, scrollTopForIndex, virtualWindow } from '../map/timelineVirtual';
import { TimelineMap, tileErrorTransition } from './TimelineMap';
import {
  confidenceLabel,
  eventLabel,
  formatTimelineDateTime,
  KNOWN_EVENTS,
  LOCATION_COORDINATE_FALLBACK,
  mapLocationLabel,
  trajectoryKey,
  trajectorySamples,
  type LogItem,
  type TrajectorySample,
} from './timelineShared';

export { tileErrorTransition };

type Props = { includePrivate?: boolean; players: Player[]; refreshKey: number };
type TimelineState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: PlayerTimelineResponse }
  | { kind: 'not-found' }
  | { kind: 'error'; message: string };
type LogRow = { item: LogItem; separator: string };

const MAX_RANGE_MS = 31 * 24 * 60 * 60 * 1000;
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
  const [playMode, setPlayMode] = useState<PlayMode>('index');
  const [loadingOlder, setLoadingOlder] = useState(false);
  const requestID = useRef(0);
  const lastRefreshKey = useRef(refreshKey);
  const cursorIndexRef = useRef(cursorIndex);
  cursorIndexRef.current = cursorIndex;
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
  const mayBeTruncated = state.kind === 'ready' && (
    state.data.events.length >= 500
    || state.data.trajectories.length >= 500
    || (includePrivate && state.data.private_samples.length >= 500)
    || (typeof state.data.event_total === 'number' && state.data.event_total > state.data.events.length)
    || (typeof state.data.trajectory_total === 'number' && state.data.trajectory_total > state.data.trajectories.length)
    || (includePrivate && typeof state.data.private_sample_total === 'number' && state.data.private_sample_total > state.data.private_samples.length)
  );
  const truncationDetail = state.kind === 'ready' ? [
    typeof state.data.event_total === 'number' ? `事件 ${state.data.events.length}/${state.data.event_total}` : null,
    typeof state.data.trajectory_total === 'number' ? `轨迹 ${state.data.trajectories.length}/${state.data.trajectory_total}` : null,
    includePrivate && typeof state.data.private_sample_total === 'number' ? `私有 ${state.data.private_samples.length}/${state.data.private_sample_total}` : null,
  ].filter(Boolean).join(' · ') : '';
  const canLoadOlder = state.kind === 'ready' && mayBeTruncated && items.length > 0;

  function oldestLoadedISO(data: PlayerTimelineResponse): string | undefined {
    const times = [
      ...data.events.map((e) => Date.parse(e.occurred_at)),
      ...data.trajectories.map((t) => Date.parse(t.observed_at)),
      ...(includePrivate ? data.private_samples.map((p) => Date.parse(p.observed_at)) : []),
    ].filter((t) => Number.isFinite(t));
    if (!times.length) return undefined;
    return new Date(Math.min(...times)).toISOString();
  }

  function mergeOlderPage(current: PlayerTimelineResponse, older: PlayerTimelineResponse): PlayerTimelineResponse {
    const eventIDs = new Set(current.events.map((e) => e.id));
    const trajKeys = new Set(current.trajectories.map((t) => `${t.user_id}:${t.segment_id}:${t.observed_at}:${t.source_ref}`));
    const privKeys = new Set(current.private_samples.map((p) => `${p.user_id}:${p.observed_at}:${p.source_ref}`));
    const events = [...older.events.filter((e) => !eventIDs.has(e.id)), ...current.events]
      .sort((a, b) => Date.parse(a.occurred_at) - Date.parse(b.occurred_at) || a.id.localeCompare(b.id));
    const trajectories = [
      ...older.trajectories.filter((t) => !trajKeys.has(`${t.user_id}:${t.segment_id}:${t.observed_at}:${t.source_ref}`)),
      ...current.trajectories,
    ].sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at));
    const private_samples = [
      ...older.private_samples.filter((p) => !privKeys.has(`${p.user_id}:${p.observed_at}:${p.source_ref}`)),
      ...current.private_samples,
    ].sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at));
    return {
      ...current,
      events,
      trajectories,
      private_samples,
      event_total: Math.max(current.event_total ?? events.length, older.event_total ?? 0),
      trajectory_total: Math.max(current.trajectory_total ?? trajectories.length, older.trajectory_total ?? 0),
      private_sample_total: Math.max(current.private_sample_total ?? private_samples.length, older.private_sample_total ?? 0),
    };
  }

  async function loadOlder() {
    if (state.kind !== 'ready' || loadingOlder || !selectedID) return;
    const before = oldestLoadedISO(state.data);
    const parsedStart = parseLocalDateTime(start).date;
    if (!before || !parsedStart) return;
    const beforeMS = Date.parse(before);
    if (!Number.isFinite(beforeMS) || beforeMS <= parsedStart.getTime()) return;
    setLoadingOlder(true);
    try {
      const older = await getPlayerTimeline(selectedID, parsedStart.toISOString(), before, 500, undefined, includePrivate);
      setState((prev) => {
        if (prev.kind !== 'ready') return prev;
        const prevCount = mergeTimeline(prev.data, includePrivate).length;
        const merged = mergeOlderPage(prev.data, older);
        const nextCount = mergeTimeline(merged, includePrivate).length;
        const added = Math.max(0, nextCount - prevCount);
        queueMicrotask(() => setCursorIndex((idx) => idx + added));
        return { kind: 'ready', data: merged };
      });
    } catch (error: unknown) {
      setState({ kind: 'error', message: error instanceof Error ? error.message : '加载更早记录失败。' });
    } finally {
      setLoadingOlder(false);
    }
  }

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
    let cancelled = false;
    let timer = 0;
    let current = cursorIndexRef.current;
    const step = () => {
      const { index, done } = nextCursorIndex(current, items.length);
      if (index === current && done) {
        setPlaying(false);
        return;
      }
      const delay = playStepDelayMs(playMode, speed, items[current]?.at, items[index]?.at);
      timer = window.setTimeout(() => {
        if (cancelled) return;
        current = index;
        setCursorIndex(index);
        if (done) {
          setPlaying(false);
          return;
        }
        step();
      }, delay);
    };
    step();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [playing, speed, playMode, items]);

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
            playMode={playMode}
            playing={playing}
            speed={speed}
            onPlayModeChange={setPlayMode}
            onPlayingChange={setPlaying}
            onSpeedChange={setSpeed}
            selected={Boolean(selectedID)}
          />
          {!selectedID ? <EmptyState icon={<Compass size={28} />} text="选择玩家后查看轨迹和事件。" /> : null}
          {state.kind === 'loading' ? <div className="timeline-skeleton" role="status" aria-label="正在加载时间轴"><span /><span /><span /></div> : null}
          {state.kind === 'not-found' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> 该玩家已不在观察记录中。</div> : null}
          {state.kind === 'error' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> {state.message}</div> : null}
          {state.kind === 'ready' && items.length === 0 ? <EmptyState icon={<Radio size={28} />} text="当前时间范围没有观察记录。" /> : null}
          {mayBeTruncated ? (
            <div className="timeline-alert timeline-alert--info" role="status">
              <AlertTriangle size={18} />
              <span>
                默认加载时间范围内<strong>最近</strong>最多 500 条/类{truncationDetail ? `（${truncationDetail}）` : ''}。
                {canLoadOlder ? ' 可继续加载更早证据。' : ' 可缩小时间范围查看完整证据。'}
              </span>
              {canLoadOlder ? (
                <button type="button" className="timeline-load-older" disabled={loadingOlder} onClick={() => void loadOlder()}>
                  {loadingOlder ? '加载中…' : '加载更早'}
                </button>
              ) : null}
            </div>
          ) : null}
          {state.kind === 'ready' && items.length > 0 ? (
            <TimelineSpineList
              activeIndex={activeIndex}
              locationNames={locationNames}
              onSelect={seekCursor}
              rows={rows}
              segmentNames={segmentNames}
            />
          ) : null}
        </div>
      </div>
    </section>
  );
}

function TimelineSpineList({
  activeIndex,
  locationNames,
  onSelect,
  rows,
  segmentNames,
}: {
  activeIndex: number;
  locationNames: Map<string, string>;
  onSelect: (index: number) => void;
  rows: LogRow[];
  segmentNames: Map<string, string>;
}) {
  const parentRef = useRef<HTMLDivElement>(null);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(400);
  const win = useMemo(
    () => virtualWindow(rows.length, scrollTop, viewportHeight, DEFAULT_ROW_ESTIMATE_PX),
    [rows.length, scrollTop, viewportHeight],
  );

  useEffect(() => {
    const el = parentRef.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const apply = () => setViewportHeight(el.clientHeight || 400);
    apply();
    const ro = new ResizeObserver(apply);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Keep the active row inside the list scrollport only (map stays put).
  useEffect(() => {
    const el = parentRef.current;
    if (!el || rows.length === 0) return;
    const estimate = DEFAULT_ROW_ESTIMATE_PX;
    const rowTop = activeIndex * estimate;
    const rowBottom = rowTop + estimate;
    const viewTop = el.scrollTop;
    const viewBottom = viewTop + el.clientHeight;
    if (rowTop < viewTop || rowBottom > viewBottom) {
      el.scrollTop = scrollTopForIndex(activeIndex, el.clientHeight, estimate);
    }
  }, [activeIndex, rows.length]);

  const slice = rows.slice(win.start, win.end);

  return (
    <div
      className="timeline-spine-window"
      data-testid="timeline-spine-window"
      ref={parentRef}
      onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
    >
      <div className="timeline-spine-spacer" style={{ height: win.totalHeight }}>
        <ol
          className="timeline-spine timeline-spine--virtual"
          aria-label="按时间排序的观察记录"
          style={{ transform: `translateY(${win.offsetTop}px)` }}
        >
          {slice.map(({ item, separator }, offset) => {
            const index = win.start + offset;
            return (
              <TimelineEntry
                active={index === activeIndex}
                index={index}
                key={item.key}
                item={item}
                locationLabel={item.kind === 'trajectory' ? locationNames.get(trajectoryKey(item.item)) : undefined}
                onSelect={onSelect}
                segmentLabel={item.kind === 'trajectory' ? segmentNames.get(item.item.segment_id) : undefined}
                separator={separator}
                setSize={rows.length}
              />
            );
          })}
        </ol>
      </div>
    </div>
  );
}

function TimelineEntry({
  active,
  index,
  item,
  locationLabel,
  onSelect,
  segmentLabel,
  separator,
  setSize,
}: {
  active: boolean;
  index: number;
  item: LogItem;
  locationLabel?: string;
  onSelect: (index: number) => void;
  segmentLabel?: string;
  separator: string;
  setSize: number;
}) {
  return <>
    {separator ? <li className="timeline-separator" role="separator"><span>{separator}</span></li> : null}
    <li
      className={`timeline-entry timeline-entry--${item.kind} ${active ? 'is-active' : ''}`}
      aria-setsize={setSize}
      aria-posinset={index + 1}
    >
      <time dateTime={item.at}>{formatTimelineDateTime(item.at)}</time>
      {item.kind === 'event' ? <EventDetail event={item.item} /> : null}
      {item.kind === 'trajectory' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>位置采样</strong><SourceBadge source="palworld_rest" /></div><dl className="telemetry"><div><dt>地图位置</dt><dd>{locationLabel ?? LOCATION_COORDINATE_FALLBACK}</dd></div><div><dt>坐标</dt><dd>{item.item.x}, {item.item.y}</dd></div><div><dt>轨迹段</dt><dd>{segmentLabel ?? '未分段'}</dd></div><div><dt>延迟</dt><dd>{item.item.ping} ms</dd></div><div><dt>等级</dt><dd>{item.item.level}</dd></div></dl></div> : null}
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
