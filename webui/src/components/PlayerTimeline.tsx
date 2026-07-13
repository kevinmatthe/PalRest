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

const MAX_RANGE_MS = 31 * 24 * 60 * 60 * 1000;
const EVIDENCE_GAP_MS = 15 * 60 * 1000;
const KNOWN_EVENTS = new Set(['player_joined', 'player_left', 'player_attribute_changed']);

function localInputValue(date: Date) {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

function defaultRange() {
  const end = new Date();
  return { start: localInputValue(new Date(end.getTime() - 24 * 60 * 60 * 1000)), end: localInputValue(end) };
}

function validateRange(start: string, end: string) {
  const startMS = new Date(start).getTime();
  const endMS = new Date(end).getTime();
  if (!Number.isFinite(startMS) || !Number.isFinite(endMS)) return 'Enter a valid start and end time.';
  if (endMS <= startMS) return 'End must be after start.';
  if (endMS - startMS > MAX_RANGE_MS) return 'The selected range cannot exceed 31 days.';
  return '';
}

function mergeTimeline(data: PlayerTimelineResponse): LogItem[] {
  const merged: LogItem[] = [];
  data.events.forEach((item, index) => merged.push({ kind: 'event', at: item.occurred_at, key: `event:${item.id || index}`, item }));
  data.trajectories.forEach((item, index) => merged.push({ kind: 'trajectory', at: item.observed_at, key: `trajectory:${item.segment_id}:${item.observed_at}:${index}`, item }));
  data.private_samples.forEach((item, index) => merged.push({ kind: 'private', at: item.observed_at, key: `private:${item.observed_at}:${item.source_ref}:${index}`, item }));
  return merged.sort((a, b) => Date.parse(a.at) - Date.parse(b.at) || a.key.localeCompare(b.key));
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
    return players.filter((player) => [player.name, player.account_name, player.user_id, player.player_id].some((value) => value.toLowerCase().includes(term)));
  }, [players, search]);

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
    void getPlayerTimeline(selectedID, new Date(start).toISOString(), new Date(end).toISOString(), 500, controller.signal)
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
          {state.kind === 'ready' && items.length > 0 ? <ol className="timeline-spine" aria-label="Chronological observations">
            {items.map((item, index) => {
              const previous = items[index - 1];
              const separator = previous ? separatorText(previous, item) : '';
              return <TimelineEntry key={item.key} item={item} separator={separator} />;
            })}
          </ol> : null}
        </div>
      </div>
    </section>
  );
}

function separatorText(previous: LogItem, current: LogItem) {
  if (current.kind === 'trajectory' && previous.kind === 'trajectory' && current.item.segment_id !== previous.item.segment_id) return 'New observation segment — no path inferred';
  if (Date.parse(current.at) - Date.parse(previous.at) > EVIDENCE_GAP_MS) return 'Observation gap — no path inferred';
  return '';
}

function TimelineEntry({ item, separator }: { item: LogItem; separator: string }) {
  return <>
    {separator ? <li className="timeline-separator" role="separator"><span>{separator}</span></li> : null}
    <li className={`timeline-entry timeline-entry--${item.kind}`}>
      <time dateTime={item.at}>{formatExactDateTime(item.at)}</time>
      {item.kind === 'event' ? <EventDetail event={item.item} /> : null}
      {item.kind === 'trajectory' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>Position observation</strong><SourceBadge source="palworld_rest" /></div><dl className="telemetry"><div><dt>Coordinates · Admin private</dt><dd>{item.item.x}, {item.item.y}</dd></div><div><dt>Segment</dt><dd>{item.item.segment_id || 'Unassigned'}</dd></div><div><dt>Ping</dt><dd>{item.item.ping} ms</dd></div><div><dt>Level</dt><dd>{item.item.level}</dd></div></dl></div> : null}
      {item.kind === 'private' ? <div className="timeline-detail"><div className="timeline-title-row"><strong>Private player sample</strong><SourceBadge source="guard" /></div><dl className="telemetry"><div><dt>IP · Admin private</dt><dd>{item.item.ip || 'Unavailable'}</dd></div><div><dt>Ping</dt><dd>{item.item.ping} ms</dd></div><div><dt>Level</dt><dd>{item.item.level}</dd></div><div><dt>Buildings</dt><dd>{item.item.building_count}</dd></div></dl></div> : null}
    </li>
  </>;
}

function EventDetail({ event }: { event: TimelineEvent }) {
  const known = KNOWN_EVENTS.has(event.event_type) && event.summary !== 'unsupported event payload';
  const occurredDiffers = event.observed_at !== event.occurred_at;
  return <div className="timeline-detail">
    <div className="timeline-title-row"><strong>{known ? event.summary : 'Unsupported event'}</strong><SourceBadge source={event.source} /><span className="confidence-badge">{titleCase(event.confidence)}</span></div>
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
