import { memo, useId, useState } from 'react';
import { ArrowUpRight, ChevronDown, Compass, MapPin } from 'lucide-react';
import type { ProgressChange } from '../api';
import type { JourneyEdge, JourneyHeatCell, JourneyMetric, JourneySummary } from '../map/journeyTypes';
import { PROGRESS_METRICS } from '../map/workspaceProgress';
import { JourneyTrend } from './JourneyTrend';
import '../journey.css';

type Props = { summary: JourneySummary; name: string; loading: boolean; error?: string; onSeek: (time: number) => void; onFocus: (cell: JourneyHeatCell) => void; onRetry: () => void };
const WARNINGS: Record<string, string> = {
  position_continuity_unknown: '部分位置观测的连续性信息不足，仅保留观测点和最后位置，不据此推断移动、停留或活动。',
  timeline_truncated: '位置记录未完整加载，仅汇总已加载的有效观测。',
  progress_truncated: '进度记录未完整加载，变化合计不代表整个窗口。',
  progress_boundary: '进度存在世界、口径或存档边界，不跨边界比较。',
  insufficient_observations: '有效位置观测不足，尚不能判断这段时间的活动。',
  stale_positions: '最近位置观测已陈旧，不推测此后的移动或在线状态。',
  data_unavailable: '部分观测数据尚不可用，未知区间不计作活动。',
  invalid_input: '观察窗口无效，暂时无法生成小结。',
};
const UNKNOWN_METRIC: JourneyMetric = { status: 'unknown', delta: null, latestValue: null, changes: [], runs: [] };
const number = (value: number) => value.toLocaleString('zh-CN', { maximumFractionDigits: 0 });
const signed = (value: number) => `${value > 0 ? '+' : ''}${number(value)}`;
const time = (value: number) => Number.isFinite(value) ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }) : '时间未知';
function duration(value: number) {
  const seconds = Math.max(0, Math.floor(value / 1000));
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.floor(seconds / 60);
  return minutes < 60 ? `${minutes} 分钟${seconds % 60 ? ` ${seconds % 60} 秒` : ''}` : `${Math.floor(minutes / 60)} 小时${minutes % 60 ? ` ${minutes % 60} 分钟` : ''}`;
}
function TimeRange({ start, end }: { start: number; end: number }) {
  return <span className="journey-time-range">{time(start)} — {time(end)}</span>;
}
function EdgeEvidence({ edges }: { edges: JourneyEdge[] }) {
  const [open, setOpen] = useState(false);
  return <details className="journey-source-evidence" onToggle={event => setOpen(event.currentTarget.open)}><summary>位置观测证据 · {edges.length} 个区间</summary>
    {open ? <ol>{edges.map((edge, index) => <li key={`${edge.start}-${index}`}>
      <TimeRange start={edge.start} end={edge.end} />
      <span>有效观测 {duration(edge.durationMs)} · 路径 {number(edge.distance)} 游戏单位</span>
      <small>来源 {edge.from.sourceRef} → {edge.to.sourceRef}</small>
      <small>位置 ({number(edge.from.x)}, {number(edge.from.y)}) → ({number(edge.to.x)}, {number(edge.to.y)})</small>
    </li>)}</ol> : null}
  </details>;
}
function ChangeEvidence({ change, onSeek }: { change: ProgressChange; onSeek: Props['onSeek'] }) {
  const label = PROGRESS_METRICS.find(metric => metric.key === change.metric)?.label ?? '进度';
  return <li className="journey-change">
    <strong>{label} {change.before} → {change.after} <em>{signed(change.delta)}</em></strong>
    <TimeRange start={Date.parse(change.interval_start)} end={Date.parse(change.interval_end)} />
    <small>变化 #{change.id} · 快照 #{change.previous_checkpoint_id} → #{change.checkpoint_id} · 存档观测</small>
    {change.added?.length ? <p>新增 ID：<span>{change.added.join('、')}</span></p> : null}
    {change.removed?.length ? <p>移除 ID：<span>{change.removed.join('、')}</span></p> : null}
    {!Array.isArray(change.added) || !Array.isArray(change.removed) ? <p>此记录未保存增减明细，仅展示已保存的数量变化。</p> : null}
    <button type="button" onClick={() => onSeek(Date.parse(change.interval_end))} aria-label={`回看变化 #${change.id} 的区间终点`}>回看区间终点 <ArrowUpRight size={15} aria-hidden="true" /></button>
  </li>;
}
function Milestone({ change, onSeek }: { change: ProgressChange; onSeek: Props['onSeek'] }) {
  const [open, setOpen] = useState(false);
  const label = change.metric === 'level' ? `存档等级提升 ${change.before} → ${change.after}`
    : `${change.metric === 'paldeck' ? '图鉴' : '传送点'}新增 ${change.added?.length ?? 0} 项`;
  return <li><details onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>{label}</summary>
    <TimeRange start={Date.parse(change.interval_start)} end={Date.parse(change.interval_end)} />
    {open ? <ol><ChangeEvidence change={change} onSeek={onSeek} /></ol> : null}
  </details></li>;
}

function MetricCard({ label, metric, start, end, onSeek }: { label: string; metric: JourneyMetric; start: number; end: number; onSeek: Props['onSeek'] }) {
  const [open, setOpen] = useState(false);
  const evidenceID = useId();
  const latest = metric.runs.at(-1)?.at(-1);
  const unavailable = metric.status === 'boundary' || metric.latestValue !== null || metric.runs.some(run => run.length) ? '不可比较' : '未采集';
  const value = metric.delta === null ? unavailable : signed(metric.delta);
  return <section className="journey-metric" aria-label={`${label}趋势与证据`}>
    <button type="button" className="journey-metric-heading" aria-expanded={open} aria-controls={evidenceID} onClick={() => setOpen(value => !value)}>
      <span><span>{label}</span><small>{metric.delta === null ? '窗口变化' : metric.status === 'partial' ? '已确认变化合计' : '观测区间变化'}</small></span>
      <strong className={metric.delta === null ? 'is-unknown' : ''}>{value}</strong><ChevronDown size={15} className={open ? 'is-open' : ''} aria-hidden="true" />
    </button>
    {metric.latestValue !== null ? <p className="journey-latest">最近保存观测 <b>{number(metric.latestValue)}</b>{latest ? <time dateTime={new Date(latest.time).toISOString()}>{time(latest.time)} · 存档导入</time> : null}</p> : null}
    <JourneyTrend label={label} runs={metric.runs} start={start} end={end} showTable={open} />
    {open ? <div id={evidenceID} className="journey-metric-evidence">
      {metric.status === 'partial' ? <p>仅合计窗口内已确认、可比较的变化区间；缺失部分不补零。</p> : null}
      {metric.status === 'boundary' ? <p>存在比较边界，不能合并成整个窗口的变化。</p> : null}
      {metric.changes.length ? <ol>{metric.changes.map(change => <ChangeEvidence key={change.id} change={change} onSeek={onSeek} />)}</ol> : <p>暂无可比较的保存变化区间。</p>}
    </div> : null}
  </section>;
}

export const WorkspaceJourney = memo(function WorkspaceJourney({ summary, name, loading, error, onSeek, onFocus, onRetry }: Props) {
  const [open, setOpen] = useState(true);
  const bodyID = useId();
  const { position } = summary;
  const coverage = Math.round(position.coverage * 100);
  const exploration = summary.inferences.find(inference => inference.kind === 'exploration');
  const changes = PROGRESS_METRICS.flatMap(metric => (summary.metrics[metric.key]?.changes ?? []));
  const topRegions = [...summary.heat].sort((a, b) => b.durationMs - a.durationMs).slice(0, 3);
  return <section className="world-journey" aria-label={`${name}的观察窗口小结`}>
    <button type="button" className="journey-heading" aria-expanded={open} aria-controls={bodyID} onClick={() => setOpen(value => !value)}>
      <Compass size={19} aria-hidden="true" /><span><strong>观察窗口小结</strong><small>{name} · 由实际观测汇总</small></span><ChevronDown size={17} className={open ? 'is-open' : ''} aria-hidden="true" />
    </button>
    {open ? <div id={bodyID} className="journey-body">
      <p className="journey-window"><TimeRange start={summary.start} end={summary.end} /></p>
      {loading ? <p role="status">正在读取窗口观测…</p> : null}
      {error ? <div className="journey-warning" role="alert"><p>{error}</p><button type="button" onClick={onRetry}>重试</button></div> : null}
      <section className="journey-coverage" aria-label="位置观测覆盖">
        <div><span>有效位置观测</span><strong>{duration(position.observedMs)}</strong></div>
        <div className="journey-coverage-bar" role="meter" aria-label="位置观测覆盖率" aria-valuemin={0} aria-valuemax={100} aria-valuenow={coverage} aria-valuetext={`${coverage}% 的窗口有有效位置观测`}><span style={{ width: `${coverage}%` }} /></div>
        <p><span>覆盖 {coverage}%</span><span>未知 {duration(position.unknownMs)}</span></p>
      </section>
      <p className="journey-caption">已加载位置观测 {position.sampleCount} 个</p>
      {position.lastObservation ? <p className="journey-caption">最后观测坐标 ({number(position.lastObservation.x)}, {number(position.lastObservation.y)})</p> : null}
      <dl className="journey-facts">
        <div><dt>已观测路径</dt><dd>{position.edges.length ? <><strong>{number(position.pathLength)}</strong><small>游戏单位</small></> : <span>暂无有效观测对</span>}</dd></div>
        <div><dt>REST 等级观测变化</dt><dd>{position.level ? <><strong>{position.level.from} → {position.level.to}</strong><small>变化 {signed(position.level.delta)}</small></> : <span>不可比较</span>}</dd></div>
      </dl>
      {position.level ? <p className="journey-caption">REST 等级观测 <TimeRange start={position.level.start} end={position.level.end} /></p> : null}
      <p className="journey-caption">{position.ageMs === null || position.asOf === null ? '尚无位置观测' : <>最近位置观测 {time(position.asOf)}<br />距窗口终点 {duration(position.ageMs)}</>}</p>
      {summary.warnings.length ? <ul className="journey-warnings">{summary.warnings.map(warning => <li key={warning}>{WARNINGS[warning] ?? '部分证据不完整，请结合原始观测查看。'}</li>)}</ul> : null}
      <div className="journey-section-heading"><h3>成长观测</h3><span>保存的变化区间</span></div>
      <p className="journey-caption">实点为观测，虚线只表示可比较；空白处没有补值。</p>
      <div className="journey-metrics">{PROGRESS_METRICS.map(metric => <MetricCard key={metric.key} label={metric.label} metric={summary.metrics[metric.key] ?? UNKNOWN_METRIC} start={summary.start} end={summary.end} onSeek={onSeek} />)}</div>
      <p className="journey-caption">拥有数量变化不等于捕获。变化发生在两次存档观测之间，未关联具体地点。</p>
      <section className="journey-milestones" aria-label="成长里程碑">
        <div className="journey-section-heading"><h3>成长里程碑</h3><span>实际保存的成长</span></div>
        <p className="journey-caption">仅展示已确认的局部区间；不代表整个窗口的净增长，也不确定变化的准确时刻或地点。</p>
        {summary.milestones?.length ? <ol>{summary.milestones.map(change => <Milestone key={`${change.metric}-${change.id}`} change={change} onSeek={onSeek} />)}</ol> : <p className="journey-caption">暂无可确认的成长里程碑。</p>}
      </section>
      <section className="journey-inference" aria-label="活动线索">
        <span className="journey-kicker">活动线索 · 规则推断</span>
        {exploration ? <details><summary>可能有探索活动</summary>
          <p>有效观测 {duration(exploration.observedMs)} · 覆盖 {Math.round(exploration.coverage * 100)}% · 移动 {duration(exploration.movingMs)} · 新增传送点 {exploration.unlockedCount}</p>
          <p>规则 {exploration.ruleVersion}：有效观测 ≥ 5 分钟，覆盖率 ≥ 60%，移动 ≥ 2 分钟，新增传送点 ≥ 1；记录完整且进度无比较边界。</p>
          <ol>{exploration.changeIDs.map(id => {
            const change = changes.find(item => item.id === id);
            return change ? <ChangeEvidence key={id} change={change} onSeek={onSeek} /> : <li key={id}>变化 #{id} · 证据暂不可用</li>;
          })}</ol>
          <EdgeEvidence edges={exploration.edges} />
        </details> : <p>证据不足，暂不判断活动类型</p>}
      </section>
      <section className="journey-regions" aria-label="主要停留区域">
        <div className="journey-section-heading"><h3>主要停留区域</h3><span>按有效时长</span></div>
        <p className="journey-caption">近似网格区域，仅累计有效静止观测时长。</p>
        {topRegions.length ? <ol>{topRegions.map((cell, index) => <li key={cell.id}>
          <button type="button" aria-label={`定位近似网格区域 ${index + 1}`} onClick={() => onFocus(cell)}><span className="journey-region-rank">0{index + 1}</span><span><strong>近似网格区域 {index + 1}</strong><small>{duration(cell.durationMs)}</small></span><MapPin size={17} aria-hidden="true" /></button>
          <TimeRange start={cell.start} end={cell.end} /><EdgeEvidence edges={cell.edges} />
        </li>)}</ol> : <p className="journey-caption">尚无有效的停留区域观测。</p>}
      </section>
      <EdgeEvidence edges={position.edges} />
    </div> : null}
  </section>;
});
