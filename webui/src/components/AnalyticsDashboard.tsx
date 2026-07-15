import { useEffect, useMemo, useState } from 'react';
import { Activity, Clock3, TrendingUp, Users } from 'lucide-react';
import {
  getAnalyticsActivity,
  getAnalyticsBehavior,
  getAnalyticsSummary,
  type AnalyticsActivity,
  type AnalyticsBehaviorRanking,
  type AnalyticsSummary,
  type BehaviorRankRange,
  type BehaviorRankSort,
  type Player,
} from '../api';
import { formatDateTime, formatDuration } from '../utils';
import { ActivityChart } from './ActivityChart';

type Period = 'today' | 'week';
type Range = '7d' | '30d';

const DOMINANT_ZH: Record<string, string> = {
  traveling: '跑图',
  local: '局部',
  stationary: '挂机',
  unknown: '未知',
};

export function AnalyticsDashboard({ players, refreshKey }: { players: Player[]; refreshKey: number }) {
  const [rankingPeriod, setRankingPeriod] = useState<Period>('today');
  const [range, setRange] = useState<Range>('7d');
  const [selectedUserID, setSelectedUserID] = useState('');
  const [playerQuery, setPlayerQuery] = useState('');
  const [summary, setSummary] = useState<AnalyticsSummary>();
  const [summaryKey, setSummaryKey] = useState<Period>();
  const [serverActivity, setServerActivity] = useState<AnalyticsActivity>();
  const [serverKey, setServerKey] = useState<Range>();
  const [playerActivity, setPlayerActivity] = useState<AnalyticsActivity>();
  const [playerKey, setPlayerKey] = useState('');
  const [summaryLoading, setSummaryLoading] = useState(true);
  const [serverLoading, setServerLoading] = useState(true);
  const [playerLoading, setPlayerLoading] = useState(false);
  const [summaryError, setSummaryError] = useState('');
  const [serverError, setServerError] = useState('');
  const [playerError, setPlayerError] = useState('');
  const [behaviorRange, setBehaviorRange] = useState<BehaviorRankRange>('today');
  const [behaviorSort, setBehaviorSort] = useState<BehaviorRankSort>('traveling');
  const [behavior, setBehavior] = useState<AnalyticsBehaviorRanking>();
  const [behaviorLoading, setBehaviorLoading] = useState(true);
  const [behaviorError, setBehaviorError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    setSummaryLoading(true);
    setSummaryError('');
    getAnalyticsSummary(rankingPeriod, controller.signal).then((next) => {
      if (!controller.signal.aborted && next.ranking_period === rankingPeriod) {
        setSummary(next);
        setSummaryKey(rankingPeriod);
      }
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setSummaryError(error instanceof Error ? error.message : '无法加载分析摘要');
    }).finally(() => {
      if (!controller.signal.aborted) setSummaryLoading(false);
    });
    return () => controller.abort();
  }, [rankingPeriod, refreshKey]);

  useEffect(() => {
    const controller = new AbortController();
    setServerLoading(true);
    setServerError('');
    getAnalyticsActivity(range, undefined, controller.signal, true).then((next) => {
      if (!controller.signal.aborted && next.range === range) {
        setServerActivity(next);
        setServerKey(range);
      }
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setServerError(error instanceof Error ? error.message : '无法加载服务器活动');
    }).finally(() => {
      if (!controller.signal.aborted) setServerLoading(false);
    });
    return () => controller.abort();
  }, [range, refreshKey]);

  useEffect(() => {
    const controller = new AbortController();
    setBehaviorLoading(true);
    setBehaviorError('');
    getAnalyticsBehavior(behaviorRange, behaviorSort, 25, controller.signal).then((next) => {
      if (!controller.signal.aborted) setBehavior(next);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setBehaviorError(error instanceof Error ? error.message : '无法加载行为排行');
    }).finally(() => {
      if (!controller.signal.aborted) setBehaviorLoading(false);
    });
    return () => controller.abort();
  }, [behaviorRange, behaviorSort, refreshKey]);

  useEffect(() => {
    const controller = new AbortController();
    const requestKey = `${range}:${selectedUserID}`;
    setPlayerError('');
    if (!selectedUserID) {
      setPlayerLoading(false);
      return () => controller.abort();
    }
    setPlayerLoading(true);
    getAnalyticsActivity(range, selectedUserID, controller.signal, false).then((next) => {
      if (!controller.signal.aborted && next.range === range && next.player?.user_id === selectedUserID) {
        setPlayerActivity(next);
        setPlayerKey(requestKey);
      }
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setPlayerError(error instanceof Error ? error.message : '无法加载玩家活动');
    }).finally(() => {
      if (!controller.signal.aborted) setPlayerLoading(false);
    });
    return () => controller.abort();
  }, [range, selectedUserID, refreshKey]);

  const visiblePlayers = useMemo(() => {
    const query = playerQuery.trim().toLowerCase();
    if (!query) return players;
    return players.filter((player) => player.user_id === selectedUserID || [player.name, player.account_name, player.user_id].some((value) => value.toLowerCase().includes(query)));
  }, [playerQuery, players, selectedUserID]);
  const errors = [summaryError, serverError, playerError, behaviorError].filter(Boolean).join(' · ');
  const matchingSummary = summaryKey === rankingPeriod ? summary : undefined;
  const matchingServer = serverKey === range ? serverActivity : undefined;
  const matchingPlayer = playerKey === `${range}:${selectedUserID}` ? playerActivity?.player : undefined;
  const firstLoad = (summaryLoading && !matchingSummary) || (serverLoading && !matchingServer);
  const asOf = matchingSummary?.as_of ?? undefined;
  const stale = asOf ? Date.now() - new Date(asOf).getTime() > 20_000 : false;

  return <section className="analytics-dashboard" aria-labelledby="analytics-heading" aria-busy={firstLoad}>
    <header className="analytics-heading">
      <div><p className="eyebrow">玩家活动</p><h2 id="analytics-heading">分析</h2></div>
      {(summaryLoading || serverLoading || playerLoading || behaviorLoading) && !firstLoad ? <span className="analytics-refreshing" role="status">正在刷新…</span> : null}
    </header>
    {firstLoad ? <div className="analytics-loading"><span className="skeleton-row" /><span>正在加载分析</span></div> : null}
    {errors ? <div className="notice analytics-notice" role="alert">{errors}</div> : null}

    <div className="analytics-metrics" aria-label="活动摘要">
      <AnalyticsMetric icon={<Activity size={19} />} label="当前在线" value={matchingSummary?.as_of ? String(matchingSummary.online_count) : '--'} detail={asOf ? `${stale ? '可能过时 · ' : ''}截至 ${formatDateTime(asOf)}` : '尚无观测'} />
      <AnalyticsMetric icon={<Clock3 size={19} />} label="今日合计" value={matchingSummary?.as_of ? formatDuration(matchingSummary.today_observed_ms) : '--'} detail="观测到的玩家时长" />
      <AnalyticsMetric icon={<TrendingUp size={19} />} label="今日峰值" value={matchingSummary?.peak_at ? String(matchingSummary.peak_count) : '--'} detail={matchingSummary?.peak_at ? formatDateTime(matchingSummary.peak_at) : '尚无峰值'} />
      <AnalyticsMetric icon={<Users size={19} />} label="今日活跃" value={matchingSummary?.as_of ? String(matchingSummary.active_players) : '--'} detail="去重观测玩家数" />
    </div>

    <div className="analytics-main">
      <div className="analytics-charts">
        <section className="panel analytics-panel">
          <PanelHeading title="服务器并发" detail={matchingServer ? `${matchingServer.start} – ${matchingServer.end} · ${matchingServer.timezone}` : '平均同时在线人数'}>
            <ToggleGroup label="并发时间范围" options={[['7d', '7 天'], ['30d', '30 天']]} value={range} onChange={(value) => setRange(value as Range)} />
          </PanelHeading>
          {matchingServer?.concurrency.length ? <ActivityChart kind="line" label="服务器并发" points={matchingServer.concurrency.map((point) => ({ at: point.at, value: point.average_count, max: point.max_count, coverage: point.coverage }))} /> : !serverLoading ? <div className="analytics-empty">该范围内没有并发观测数据。</div> : <SkeletonChart />}
        </section>

        <section className="panel analytics-panel">
          <PanelHeading title="玩家活动" detail="按本地日的观测时长" />
          <div className="player-picker">
            <label>查找玩家<input value={playerQuery} onChange={(event) => setPlayerQuery(event.target.value)} placeholder="搜索已知玩家" /></label>
            <label>玩家活动<select value={selectedUserID} onChange={(event) => setSelectedUserID(event.target.value)}><option value="">选择玩家</option>{visiblePlayers.map((player) => <option key={player.user_id} value={player.user_id}>{player.name || player.account_name || player.user_id}</option>)}</select></label>
          </div>
          {!selectedUserID ? <div className="analytics-empty">选择已知玩家以查看每日活动。</div>
            : matchingPlayer?.daily.length ? <ActivityChart kind="bar" label={`${matchingPlayer.name || matchingPlayer.user_id} 每日活动`} points={matchingPlayer.daily.map((point) => ({ date: point.date, value: point.observed_ms }))} />
            : !playerLoading ? <div className="analytics-empty">该玩家在此范围内没有观测活动。</div> : <SkeletonChart />}
        </section>
      </div>

      <section className="panel analytics-panel ranking-panel">
        <PanelHeading title="玩家排行" detail="观测时长">
          <ToggleGroup label="排行周期" options={[['today', '今日'], ['week', '本周']]} value={rankingPeriod} onChange={(value) => setRankingPeriod(value as Period)} />
        </PanelHeading>
        {matchingSummary?.ranking.length ? <div className="ranking-wrap"><table className="ranking-table"><caption>玩家活动排行</caption><thead><tr><th scope="col">#</th><th scope="col">玩家</th><th scope="col">时长</th></tr></thead><tbody>{matchingSummary.ranking.map((entry, index) => <tr key={entry.user_id}><td>{index + 1}</td><th scope="row"><span>{entry.name || entry.user_id}</span><small className={entry.online ? 'online' : 'offline'}>{entry.online ? '在线' : '离线'}</small></th><td>{formatDuration(entry.observed_ms)}</td></tr>)}</tbody></table></div>
          : !summaryLoading ? <div className="analytics-empty">该周期没有排行活动数据。</div> : <SkeletonChart />}
      </section>
    </div>

    <section className="panel analytics-panel behavior-ranking-panel" aria-label="轨迹行为排行">
      <PanelHeading
        title="轨迹行为排行"
        detail={behavior?.note ?? '由位置轨迹推导 · 非政策在线时长'}
      >
        <div className="behavior-rank-controls">
          <ToggleGroup
            label="行为范围"
            options={[['today', '今日'], ['7d', '7 天']]}
            value={behaviorRange}
            onChange={(value) => setBehaviorRange(value as BehaviorRankRange)}
          />
          <ToggleGroup
            label="行为排序"
            options={[
              ['traveling', '跑图'],
              ['stationary', '挂机'],
              ['radius', '活动范围'],
              ['path', '路径'],
              ['active', '观测活跃'],
            ]}
            value={behaviorSort}
            onChange={(value) => setBehaviorSort(value as BehaviorRankSort)}
          />
        </div>
      </PanelHeading>
      {behavior?.ranking.length ? (
        <div className="ranking-wrap">
          <table className="ranking-table behavior-ranking-table">
            <caption>轨迹行为排行</caption>
            <thead>
              <tr>
                <th scope="col">#</th>
                <th scope="col">玩家</th>
                <th scope="col">主导</th>
                <th scope="col">跑图</th>
                <th scope="col">挂机</th>
                <th scope="col">半径</th>
                <th scope="col">路径</th>
                <th scope="col">活跃</th>
              </tr>
            </thead>
            <tbody>
              {behavior.ranking.map((entry, index) => (
                <tr key={entry.user_id}>
                  <td>{index + 1}</td>
                  <th scope="row">
                    <span>{entry.name || entry.user_id}</span>
                    <small className={entry.online ? 'online' : 'offline'}>{entry.online ? '在线' : '离线'}</small>
                  </th>
                  <td>{DOMINANT_ZH[entry.dominant_class] ?? entry.dominant_class}</td>
                  <td>{Math.round(entry.traveling_share * 100)}%</td>
                  <td>{Math.round(entry.stationary_share * 100)}%</td>
                  <td>{Math.round(entry.radius).toLocaleString('zh-CN')}</td>
                  <td>{Math.round(entry.path_length).toLocaleString('zh-CN')}</td>
                  <td>{formatDuration(entry.observed_active_ms)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : !behaviorLoading ? (
        <div className="analytics-empty">当前范围没有足够的轨迹样本做行为排行。</div>
      ) : (
        <SkeletonChart />
      )}
    </section>
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
