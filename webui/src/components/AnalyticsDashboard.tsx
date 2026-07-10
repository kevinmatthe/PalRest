import { useEffect, useMemo, useState } from 'react';
import { Activity, Clock3, TrendingUp, Users } from 'lucide-react';
import {
  getAnalyticsActivity,
  getAnalyticsSummary,
  type AnalyticsActivity,
  type AnalyticsSummary,
  type Player,
} from '../api';
import { formatDateTime, formatDuration } from '../utils';
import { ActivityChart } from './ActivityChart';

type Period = 'today' | 'week';
type Range = '7d' | '30d';

export function AnalyticsDashboard({ players, refreshKey }: { players: Player[]; refreshKey: number }) {
  const [rankingPeriod, setRankingPeriod] = useState<Period>('today');
  const [range, setRange] = useState<Range>('7d');
  const [selectedUserID, setSelectedUserID] = useState('');
  const [playerQuery, setPlayerQuery] = useState('');
  const [summary, setSummary] = useState<AnalyticsSummary>();
  const [activity, setActivity] = useState<AnalyticsActivity>();
  const [summaryLoading, setSummaryLoading] = useState(true);
  const [activityLoading, setActivityLoading] = useState(true);
  const [summaryError, setSummaryError] = useState('');
  const [activityError, setActivityError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    setSummaryLoading(true);
    setSummaryError('');
    getAnalyticsSummary(rankingPeriod, controller.signal).then((next) => {
      if (!controller.signal.aborted) setSummary(next);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setSummaryError(error instanceof Error ? error.message : 'Could not load analytics summary');
    }).finally(() => {
      if (!controller.signal.aborted) setSummaryLoading(false);
    });
    return () => controller.abort();
  }, [rankingPeriod, refreshKey]);

  useEffect(() => {
    const controller = new AbortController();
    setActivityLoading(true);
    setActivityError('');
    getAnalyticsActivity(range, selectedUserID || undefined, controller.signal).then((next) => {
      if (!controller.signal.aborted) setActivity(next);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setActivityError(error instanceof Error ? error.message : 'Could not load analytics activity');
    }).finally(() => {
      if (!controller.signal.aborted) setActivityLoading(false);
    });
    return () => controller.abort();
  }, [range, selectedUserID, refreshKey]);

  const visiblePlayers = useMemo(() => {
    const query = playerQuery.trim().toLowerCase();
    if (!query) return players;
    return players.filter((player) => [player.name, player.account_name, player.user_id].some((value) => value.toLowerCase().includes(query)));
  }, [playerQuery, players]);
  const errors = [summaryError, activityError].filter(Boolean).join(' · ');
  const firstLoad = (summaryLoading && !summary) || (activityLoading && !activity);
  const asOf = summary?.as_of ?? undefined;
  const stale = asOf ? Date.now() - new Date(asOf).getTime() > 20_000 : false;

  return <section className="analytics-dashboard" aria-labelledby="analytics-heading" aria-busy={firstLoad}>
    <header className="analytics-heading">
      <div><p className="eyebrow">Player activity</p><h2 id="analytics-heading">Analytics</h2></div>
      {(summaryLoading || activityLoading) && !firstLoad ? <span className="analytics-refreshing" role="status">Refreshing data…</span> : null}
    </header>
    {firstLoad ? <div className="analytics-loading"><span className="skeleton-row" /><span>Loading analytics</span></div> : null}
    {errors ? <div className="notice analytics-notice" role="alert">{errors}</div> : null}

    <div className="analytics-metrics" aria-label="Activity summary">
      <AnalyticsMetric icon={<Activity size={19} />} label="Online now" value={summary?.as_of ? String(summary.online_count) : '--'} detail={asOf ? `${stale ? 'Stale · ' : ''}As of ${formatDateTime(asOf)}` : 'No observation yet'} />
      <AnalyticsMetric icon={<Clock3 size={19} />} label="Today total" value={summary?.as_of ? formatDuration(summary.today_observed_ms) : '--'} detail="Observed player time" />
      <AnalyticsMetric icon={<TrendingUp size={19} />} label="Peak today" value={summary?.peak_at ? String(summary.peak_count) : '--'} detail={summary?.peak_at ? formatDateTime(summary.peak_at) : 'No peak observed'} />
      <AnalyticsMetric icon={<Users size={19} />} label="Active today" value={summary?.as_of ? String(summary.active_players) : '--'} detail="Unique observed players" />
    </div>

    <div className="analytics-main">
      <div className="analytics-charts">
        <section className="panel analytics-panel">
          <PanelHeading title="Server concurrency" detail={activity ? `${activity.start} – ${activity.end} · ${activity.timezone}` : 'Average concurrent players'}>
            <ToggleGroup label="Concurrency range" options={[['7d', '7 days'], ['30d', '30 days']]} value={range} onChange={(value) => setRange(value as Range)} />
          </PanelHeading>
          {activity?.concurrency.length ? <ActivityChart kind="line" label="Server concurrency" points={activity.concurrency.map((point) => ({ at: point.at, value: point.average_count, max: point.max_count, coverage: point.coverage }))} /> : !activityLoading ? <div className="analytics-empty">No concurrency observations for this range.</div> : <SkeletonChart />}
        </section>

        <section className="panel analytics-panel">
          <PanelHeading title="Player activity" detail="Observed time by local day" />
          <div className="player-picker">
            <label>Find player<input value={playerQuery} onChange={(event) => setPlayerQuery(event.target.value)} placeholder="Search known players" /></label>
            <label>Player activity<select value={selectedUserID} onChange={(event) => setSelectedUserID(event.target.value)}><option value="">Select player</option>{visiblePlayers.map((player) => <option key={player.user_id} value={player.user_id}>{player.name || player.account_name || player.user_id}</option>)}</select></label>
          </div>
          {!selectedUserID ? <div className="analytics-empty">Select a known player to inspect daily activity.</div>
            : activity?.player?.daily.length ? <ActivityChart kind="bar" label={`${activity.player.name || activity.player.user_id} daily activity`} points={activity.player.daily.map((point) => ({ date: point.date, value: point.observed_ms }))} />
            : !activityLoading ? <div className="analytics-empty">No observed activity for this player and range.</div> : <SkeletonChart />}
        </section>
      </div>

      <section className="panel analytics-panel ranking-panel">
        <PanelHeading title="Player ranking" detail="Observed duration">
          <ToggleGroup label="Ranking period" options={[['today', 'Today'], ['week', 'Week']]} value={rankingPeriod} onChange={(value) => setRankingPeriod(value as Period)} />
        </PanelHeading>
        {summary?.ranking.length ? <div className="ranking-wrap"><table className="ranking-table"><caption>Player activity ranking</caption><thead><tr><th scope="col">#</th><th scope="col">Player</th><th scope="col">Duration</th></tr></thead><tbody>{summary.ranking.map((entry, index) => <tr key={entry.user_id}><td>{index + 1}</td><th scope="row"><span>{entry.name || entry.user_id}</span><small className={entry.online ? 'online' : 'offline'}>{entry.online ? 'Online' : 'Offline'}</small></th><td>{formatDuration(entry.observed_ms)}</td></tr>)}</tbody></table></div>
          : !summaryLoading ? <div className="analytics-empty">No ranking activity for this period.</div> : <SkeletonChart />}
      </section>
    </div>
  </section>;
}

function AnalyticsMetric({ icon, label, value, detail }: { icon: React.ReactNode; label: string; value: string; detail: string }) {
  return <article className="analytics-metric"><span className="analytics-metric__icon">{icon}</span><div><span>{label}</span><strong>{value}</strong><p>{detail}</p></div></article>;
}

function PanelHeading({ title, detail, children }: { title: string; detail: string; children?: React.ReactNode }) {
  return <header className="panel-header analytics-panel-heading"><div><h2>{title}</h2><p>{detail}</p></div>{children}</header>;
}

function ToggleGroup({ label, options, value, onChange }: { label: string; options: [string, string][]; value: string; onChange: (value: string) => void }) {
  return <div className="analytics-toggle" role="group" aria-label={label}>{options.map(([key, text]) => <button type="button" key={key} aria-pressed={value === key} onClick={() => onChange(key)}>{text}</button>)}</div>;
}

function SkeletonChart() {
  return <div className="analytics-skeleton" aria-hidden="true"><span className="skeleton-row" /><span className="skeleton-row" /><span className="skeleton-row" /></div>;
}
