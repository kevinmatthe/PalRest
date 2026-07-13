import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Compass, Radio, Search, ShieldAlert } from 'lucide-react';
import { ApiError, getPlayerTimeline, type Player, type PlayerTimelineResponse, type TimelineEvent } from '../api';
import { formatExactDateTime, titleCase } from '../utils';

type Props = { players: Player[]; refreshKey: number };
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

const MAX_RANGE_MS = 31 * 24 * 60 * 60 * 1000;
const KNOWN_EVENTS = new Set(['player_joined', 'player_left', 'player_attribute_changed']);
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

function mergeTimeline(data: PlayerTimelineResponse): LogItem[] {
  const merged: LogItem[] = [];
  data.events.forEach((item) => merged.push({ kind: 'event', at: item.occurred_at, key: `event:${item.id}`, item }));
  data.trajectories.forEach((item) => merged.push({ kind: 'trajectory', at: item.observed_at, key: `trajectory:${item.user_id}:${item.segment_id}:${item.observed_at}:${item.source_ref}`, item }));
  data.private_samples.forEach((item) => merged.push({ kind: 'private', at: item.observed_at, key: `private:${item.user_id}:${item.observed_at}:${item.source_ref}`, item }));
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

export function PlayerTimeline({ players, refreshKey }: Props) {
  const range = useMemo(defaultRange, []);
  const [selectedID, setSelectedID] = useState('');
  const [search, setSearch] = useState('');
  const [start, setStart] = useState(range.start);
  const [end, setEnd] = useState(range.end);
  const [state, setState] = useState<TimelineState>({ kind: 'idle' });
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
    void getPlayerTimeline(selectedID, parsedStart.toISOString(), parsedEnd.toISOString(), 500, controller.signal)
      .then((data) => {
        if (!controller.signal.aborted && requestID.current === id) setState({ kind: 'ready', data });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted || requestID.current !== id) return;
        if (error instanceof ApiError && error.status === 404) setState({ kind: 'not-found' });
        else setState({ kind: 'error', message: error instanceof Error ? error.message : 'Timeline request failed.' });
      });
    return () => controller.abort();
  }, [selectedID, start, end, refreshKey, rangeError]);

  const items = useMemo(() => state.kind === 'ready' ? mergeTimeline(state.data) : [], [state]);
  const rows = useMemo(() => annotateTrajectoryEvidence(items), [items]);
  const mayBeTruncated = state.kind === 'ready' && [state.data.events, state.data.trajectories, state.data.private_samples].some((source) => source.length >= 500);

  return (
    <section className="timeline-recorder" aria-labelledby="timeline-heading">
      <header className="timeline-heading">
        <div>
          <p className="eyebrow">Administrator field recorder</p>
          <h2 id="timeline-heading">Player observation timeline</h2>
          <p>Recorded evidence only. Gaps are never interpolated.</p>
        </div>
        <span className="timeline-private"><ShieldAlert size={16} /> Private console</span>
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
          {!selectedID ? <EmptyState icon={<Compass size={28} />} text="Select a player to open their expedition log." /> : null}
          {state.kind === 'loading' ? <div className="timeline-skeleton" role="status" aria-label="Loading timeline"><span /><span /><span /></div> : null}
          {state.kind === 'not-found' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> This player is no longer known to the observation store.</div> : null}
          {state.kind === 'error' ? <div className="timeline-alert" role="alert"><AlertTriangle size={18} /> {state.message}</div> : null}
          {state.kind === 'ready' && items.length === 0 ? <EmptyState icon={<Radio size={28} />} text="No observations recorded in this range." /> : null}
          {mayBeTruncated ? <div className="timeline-alert timeline-alert--info" role="status"><AlertTriangle size={18} /> Evidence may be truncated because a source reached the 500-record response limit.</div> : null}
          {state.kind === 'ready' && items.length > 0 ? <ol className="timeline-spine" aria-label="Chronological observations">
            {rows.map(({ item, separator }) => <TimelineEntry key={item.key} item={item} separator={separator} />)}
          </ol> : null}
        </div>
      </div>
    </section>
  );
}

function TimelineEntry({ item, separator }: { item: LogItem; separator: string }) {
  return <>
    {separator ? <li className="timeline-separator" role="separator"><span>{separator}</span></li> : null}
    <li className={`timeline-entry timeline-entry--${item.kind}`}>
      <time dateTime={item.at}>{formatExactDateTime(item.at)}</time>
      {item.kind === 'event' ? <EventDetail event={item.item} /> : null}
      {item.kind === 'trajectory' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>Position observation</strong><SourceBadge source="palworld_rest" /></div><dl className="telemetry"><div><dt>Coordinates · Admin private</dt><dd>{item.item.x}, {item.item.y}</dd></div><div><dt>Segment</dt><dd>{item.item.segment_id || 'Unassigned'}</dd></div><div><dt>Ping</dt><dd>{item.item.ping} ms</dd></div><div><dt>Level</dt><dd>{item.item.level}</dd></div></dl></div> : null}
      {item.kind === 'private' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>Private player sample</strong><SourceBadge source="palworld_rest" /></div><dl className="telemetry"><div><dt>IP · Admin private</dt><dd>{item.item.ip || 'Unavailable'}</dd></div><div><dt>Ping</dt><dd>{item.item.ping} ms</dd></div><div><dt>Level</dt><dd>{item.item.level}</dd></div><div><dt>Buildings</dt><dd>{item.item.building_count}</dd></div></dl></div> : null}
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
