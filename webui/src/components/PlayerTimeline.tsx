import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Compass, Crosshair, Map as MapIcon, Radio, Route, Search, ShieldAlert } from 'lucide-react';
import { ApiError, getPlayerTimeline, type Player, type PlayerTimelineResponse, type TimelineEvent } from '../api';
import { formatExactDateTime, titleCase } from '../utils';

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

const MAX_RANGE_MS = 31 * 24 * 60 * 60 * 1000;
const KNOWN_EVENTS = new Set([
  'player_joined', 'player_left', 'player_attribute_changed',
  'guard_warning_attempted', 'guard_warning_delivered', 'guard_warning_failed',
  'enforcement_attempted', 'enforcement_succeeded', 'enforcement_failed',
]);
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
  if (!match) return { error: 'Enter a valid local date and time.' };
  const [, yearText, monthText, dayText, hourText, minuteText] = match;
  const parts = [yearText, monthText, dayText, hourText, minuteText].map(Number);
  const [year, month, day, hour, minute] = parts;
  if (year < 1000 || month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59) return { error: 'Enter a valid local date and time.' };
  const date = new Date(year, month - 1, day, hour, minute, 0, 0);
  const matchesWallTime = (candidate: Date) => candidate.getFullYear() === year && candidate.getMonth() === month - 1 && candidate.getDate() === day && candidate.getHours() === hour && candidate.getMinutes() === minute;
  if (!matchesWallTime(date)) return { error: 'This local time does not exist because the clock changes then.' };
  for (let offsetMinutes = -180; offsetMinutes <= 180; offsetMinutes += 1) {
    if (offsetMinutes === 0) continue;
    const candidate = new Date(date.getTime() + offsetMinutes * 60_000);
    if (matchesWallTime(candidate)) return { error: 'This local time is ambiguous because the clock repeats it. Choose another time.' };
  }
  return { date };
}

function validateRange(start: string, end: string) {
  const parsedStart = parseLocalDateTime(start);
  const parsedEnd = parseLocalDateTime(end);
  if (parsedStart.error) return `Start: ${parsedStart.error}`;
  if (parsedEnd.error) return `End: ${parsedEnd.error}`;
  const startMS = parsedStart.date!.getTime();
  const endMS = parsedEnd.date!.getTime();
  if (endMS <= startMS) return 'End must be after start.';
  if (endMS - startMS > MAX_RANGE_MS) return 'The selected range cannot exceed 31 days.';
  return '';
}

function mergeTimeline(data: PlayerTimelineResponse, includePrivate: boolean): LogItem[] {
  const merged: LogItem[] = [];
  data.events.forEach((item) => merged.push({ kind: 'event', at: item.occurred_at, key: `event:${item.id}`, item }));
  data.trajectories.forEach((item) => merged.push({ kind: 'trajectory', at: item.observed_at, key: `trajectory:${item.user_id}:${item.segment_id}:${item.observed_at}:${item.source_ref}`, item }));
  if (includePrivate) data.private_samples.forEach((item) => merged.push({ kind: 'private', at: item.observed_at, key: `private:${item.user_id}:${item.observed_at}:${item.source_ref}`, item }));
  return merged.sort((a, b) => Date.parse(a.at) - Date.parse(b.at) || a.key.localeCompare(b.key));
}

function annotateTrajectoryEvidence(items: LogItem[]): LogRow[] {
  let previousTrajectory: PlayerTimelineResponse['trajectories'][number] | undefined;
  return items.map((item) => {
    let separator = '';
    if (item.kind === 'trajectory') {
      if (previousTrajectory && item.item.segment_id !== previousTrajectory.segment_id) {
        separator = 'New observation segment — no path inferred';
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

export function PlayerTimeline({ includePrivate = false, players, refreshKey }: Props) {
  const range = useMemo(defaultRange, []);
  const [selectedID, setSelectedID] = useState('');
  const [search, setSearch] = useState('');
  const [start, setStart] = useState(range.start);
  const [end, setEnd] = useState(range.end);
  const [state, setState] = useState<TimelineState>({ kind: 'idle' });
  const [cursorIndex, setCursorIndex] = useState(0);
  const requestID = useRef(0);
  const rangeError = validateRange(start, end);
  const visiblePlayers = useMemo(() => {
    const term = search.trim().toLowerCase();
    if (!term) return players;
    return players.filter((player) => player.user_id === selectedID || [player.name, player.account_name, player.user_id, player.player_id].some((value) => value.toLowerCase().includes(term)));
  }, [players, search, selectedID]);

  useEffect(() => {
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
  const activeIndex = items.length ? Math.min(cursorIndex, items.length - 1) : 0;
  const mayBeTruncated = state.kind === 'ready' && [state.data.events, state.data.trajectories, includePrivate ? state.data.private_samples : []].some((source) => source.length >= 500);

  useEffect(() => {
    setCursorIndex(0);
  }, [selectedID, start, end, refreshKey, items.length]);

  return (
    <section className="timeline-recorder" aria-labelledby="timeline-heading">
      <header className="timeline-heading">
        <div>
          <p className="eyebrow">{includePrivate ? 'Administrator field recorder' : 'Public field recorder'}</p>
          <h2 id="timeline-heading">Player observation timeline</h2>
          <p>Recorded evidence only. Gaps are never interpolated.</p>
        </div>
        {includePrivate ? <span className="timeline-private"><ShieldAlert size={16} /> Private console</span> : null}
      </header>
      <div className="timeline-layout">
        <aside className="timeline-filters" aria-label="Timeline filters">
          <label className="timeline-field">
            <span>Search known players</span>
            <span className="timeline-input-with-icon"><Search size={16} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Name, account, or ID" /></span>
          </label>
          <label className="timeline-field">
            <span>Player</span>
            <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
              <option value="">Select a player…</option>
              {visiblePlayers.map((player) => <option value={player.user_id} key={player.user_id}>{player.name || player.account_name || player.user_id} · {player.account_name || player.user_id}</option>)}
            </select>
          </label>
          <label className="timeline-field"><span>Start</span><input type="datetime-local" value={start} onChange={(event) => setStart(event.target.value)} /></label>
          <label className="timeline-field"><span>End</span><input type="datetime-local" value={end} onChange={(event) => setEnd(event.target.value)} /></label>
          <p className="timeline-range-note">Local time · maximum 31 days · up to 500 records per source</p>
        </aside>
        <div className="timeline-log">
          <TimelineMap
            activeIndex={activeIndex}
            items={items}
            loading={state.kind === 'loading'}
            onCursorChange={setCursorIndex}
            selected={Boolean(selectedID)}
          />
          {!selectedID ? <EmptyState icon={<Compass size={28} />} text="Select a player to open their expedition log." /> : null}
          {state.kind === 'loading' ? <div className="timeline-skeleton" role="status" aria-label="Loading timeline"><span /><span /><span /></div> : null}
          {state.kind === 'not-found' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> This player is no longer known to the observation store.</div> : null}
          {state.kind === 'error' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> {state.message}</div> : null}
          {state.kind === 'ready' && items.length === 0 ? <EmptyState icon={<Radio size={28} />} text="No observations recorded in this range." /> : null}
          {mayBeTruncated ? <div className="timeline-alert timeline-alert--info" role="status"><AlertTriangle size={18} /> Evidence may be truncated because a source reached the 500-record response limit.</div> : null}
          {state.kind === 'ready' && items.length > 0 ? <ol className="timeline-spine" aria-label="Chronological observations">
            {rows.map(({ item, separator }, index) => <TimelineEntry active={index === activeIndex} key={item.key} item={item} onSelect={() => setCursorIndex(index)} separator={separator} />)}
          </ol> : null}
        </div>
      </div>
    </section>
  );
}

function TimelineMap({ activeIndex, items, loading, onCursorChange, selected }: { activeIndex: number; items: LogItem[]; loading: boolean; onCursorChange: (index: number) => void; selected: boolean }) {
  const samples = useMemo(() => trajectorySamples(items), [items]);
  const bounds = useMemo(() => trajectoryBounds(samples), [samples]);
  const points = useMemo<MapPoint[]>(() => {
    if (!bounds) return [];
    return samples.map((sample) => {
      const position = projectSample(sample, bounds);
      return { ...position, key: `${sample.segment_id}:${sample.observed_at}:${sample.source_ref}`, sample };
    });
  }, [bounds, samples]);
  const segments = useMemo(() => splitRouteSegments(points), [points]);
  const activeItem = items[activeIndex];
  const activeSample = latestPointAt(samples, activeItem?.at);
  const activePoint = activeSample && bounds ? projectSample(activeSample, bounds) : undefined;
  const startMS = items.length ? Math.min(...items.map((item) => Date.parse(item.at))) : 0;
  const endMS = items.length ? Math.max(...items.map((item) => Date.parse(item.at))) : 1;
  const cursorLeft = activeItem ? timelinePercent(activeItem.at, startMS, endMS) : 0;
  const activeLabel = activeItem ? activeItem.kind === 'event' ? `Cursor: ${titleCase(activeItem.item.event_type)}` : activeItem.kind === 'trajectory' ? 'Cursor: position observation' : 'Cursor: private player sample' : selected ? 'Waiting for observations' : 'No player selected';

  return <section className="timeline-map-panel" aria-label="Map replay">
    <div className="timeline-map-header">
      <div>
        <p className="eyebrow">Map replay</p>
        <h3>{activeLabel}</h3>
      </div>
      <span className="timeline-map-count"><Route size={15} /> {samples.length} positions</span>
    </div>
    <div className={`timeline-map-surface ${!points.length ? 'is-empty' : ''}`}>
      {points.length ? <svg aria-label="Trajectory map" data-testid="timeline-map" preserveAspectRatio="none" role="img" viewBox="0 0 100 64">
        <defs>
          <pattern height="8" id="timeline-grid" patternUnits="userSpaceOnUse" width="8">
            <path d="M 8 0 L 0 0 0 8" fill="none" stroke="currentColor" strokeWidth="0.18" />
          </pattern>
        </defs>
        <rect className="timeline-map-grid" height="64" width="100" />
        <path className="timeline-map-landmark" d="M0 42 C18 37 25 44 40 38 S72 29 100 35 L100 64 L0 64 Z" />
        {segments.map((segment) => <polyline className="timeline-map-route" data-testid="timeline-map-route" key={segment[0].key} points={segment.map((point) => `${point.x},${point.y}`).join(' ')} />)}
        {points.map((point) => <circle className="timeline-map-point" cx={point.x} cy={point.y} key={point.key} r="1.35" />)}
        {activePoint ? <g className="timeline-map-active" data-testid="timeline-map-active">
          <circle cx={activePoint.x} cy={activePoint.y} r="3.2" />
          <circle cx={activePoint.x} cy={activePoint.y} r="1.2" />
        </g> : null}
      </svg> : <div className="timeline-map-empty">{loading ? 'Loading trajectory evidence.' : selected ? 'No position samples in this range.' : 'Choose a player to load the replay map.'}</div>}
    </div>
    <div className="timeline-cursor">
      <div className="timeline-cursor-track" aria-hidden="true">
        {items.map((item, index) => <span className={`timeline-cursor-tick timeline-cursor-tick--${item.kind}`} key={item.key} style={{ left: `${timelinePercent(item.at, startMS, endMS)}%` }} data-active={index === activeIndex ? 'true' : undefined} />)}
        <span className="timeline-cursor-now" style={{ left: `${cursorLeft}%` }} />
      </div>
      <input aria-label="Timeline cursor" disabled={items.length < 2} max={Math.max(items.length - 1, 0)} min={0} onChange={(event) => onCursorChange(Number(event.target.value))} type="range" value={activeIndex} />
      <div className="timeline-map-meta">
        <span><MapIcon size={15} /> {activeSample ? `${activeSample.x}, ${activeSample.y}` : 'No coordinates'}</span>
        <span>{activeItem ? formatExactDateTime(activeItem.at) : 'No cursor time'}</span>
      </div>
    </div>
  </section>;
}

function TimelineEntry({ active, item, onSelect, separator }: { active: boolean; item: LogItem; onSelect: () => void; separator: string }) {
  return <>
    {separator ? <li className="timeline-separator" role="separator"><span>{separator}</span></li> : null}
    <li className={`timeline-entry timeline-entry--${item.kind} ${active ? 'is-active' : ''}`}>
      <time dateTime={item.at}>{formatExactDateTime(item.at)}</time>
      {item.kind === 'event' ? <EventDetail event={item.item} /> : null}
      {item.kind === 'trajectory' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>Position observation</strong><SourceBadge source="palworld_rest" /></div><dl className="telemetry"><div><dt>Coordinates</dt><dd>{item.item.x}, {item.item.y}</dd></div><div><dt>Segment</dt><dd>{item.item.segment_id || 'Unassigned'}</dd></div><div><dt>Ping</dt><dd>{item.item.ping} ms</dd></div><div><dt>Level</dt><dd>{item.item.level}</dd></div></dl></div> : null}
      {item.kind === 'private' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>Private player sample</strong><SourceBadge source="palworld_rest" /></div><dl className="telemetry"><div><dt>IP · Admin private</dt><dd>{item.item.ip || 'Unavailable'}</dd></div><div><dt>Ping</dt><dd>{item.item.ping} ms</dd></div><div><dt>Level</dt><dd>{item.item.level}</dd></div></dl></div> : null}
      <button className="timeline-focus" title="Move replay cursor here" type="button" onClick={onSelect}><Crosshair size={16} /></button>
    </li>
  </>;
}

function EventDetail({ event }: { event: TimelineEvent }) {
  const known = KNOWN_EVENTS.has(event.event_type) && event.summary !== 'unsupported event payload';
  const occurredDiffers = event.observed_at !== event.occurred_at;
  return <div className="timeline-detail">
    <div className="timeline-title-row"><strong>{known ? titleCase(event.event_type) : 'Unsupported event'}</strong><SourceBadge source={event.source} /><span className="confidence-badge">{titleCase(event.confidence)}</span></div>
    {occurredDiffers ? <dl className="telemetry timeline-times"><div><dt>Occurred</dt><dd>{formatExactDateTime(event.occurred_at)}</dd></div><div><dt>Observed</dt><dd>{formatExactDateTime(event.observed_at)}</dd></div></dl> : null}
  </div>;
}

function SourceBadge({ source }: { source: string }) {
  const normalized = source.toLowerCase();
  const label = normalized.includes('snapshot') ? 'Snapshot' : normalized.includes('rest') ? 'REST' : 'Guard';
  return <span className={`source-badge source-badge--${label.toLowerCase()}`}>{label}</span>;
}

function EmptyState({ icon, text }: { icon: React.ReactNode; text: string }) {
  return <div className="timeline-empty">{icon}<p>{text}</p></div>;
}
