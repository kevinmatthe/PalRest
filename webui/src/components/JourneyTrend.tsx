import { useId, useState } from 'react';
import type { JourneyTrendPoint } from '../map/journeyTypes';
import '../journey.css';

type Props = { label: string; start: number; end: number; runs: JourneyTrendPoint[][]; showTable?: boolean };
const observationTime = (time: number) => new Date(time).toLocaleString('zh-CN', { hour12: false });

// Keep actual endpoint/extreme observations per horizontal pixel bucket. Runs remain disconnected.
function chartRuns(runs: JourneyTrendPoint[][], start: number, end: number, count: number) {
  if (count <= 1000) return runs;
  const buckets = new Map<number, { first: JourneyTrendPoint; last: JourneyTrendPoint; min: JourneyTrendPoint; max: JourneyTrendPoint }>();
  for (const run of runs) for (const point of run) {
    const key = Math.min(263, Math.max(0, Math.floor((point.time - start) / (end - start || 1) * 264)));
    const bucket = buckets.get(key);
    if (!bucket) buckets.set(key, { first: point, last: point, min: point, max: point });
    else { bucket.last = point; if (point.value < bucket.min.value) bucket.min = point; if (point.value > bucket.max.value) bucket.max = point; }
  }
  const selected = new Set([...buckets.values()].flatMap(bucket => [bucket.first, bucket.last, bucket.min, bucket.max].map(point => point.checkpointID)));
  return runs.map(run => run.filter(point => selected.has(point.checkpointID))).filter(run => run.length);
}

export function JourneyTrend({ label, start, end, runs, showTable = false }: Props) {
  const descriptionID = useId();
  const [requestedPage, setPage] = useState(0);
  const points = runs.flat();
  const pageCount = Math.max(1, Math.ceil(points.length / 100));
  const page = Math.min(requestedPage, pageCount - 1);
  const rows = runs.flatMap((run, index) => run.map((point, pointIndex) => ({ point, index, pointIndex }))).slice(page * 100, (page + 1) * 100);
  if (!points.length) return <p className="journey-empty-trend">尚无可绘制的观测</p>;
  const displayed = chartRuns(runs, start, end, points.length);
  const displayedCount = displayed.reduce((sum, run) => sum + run.length, 0);
  const values = points.map(point => point.value);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const x = (point: JourneyTrendPoint) => 8 + ((point.time - start) / (end - start || 1)) * 264;
  const y = (point: JourneyTrendPoint) => max === min ? 28 : 46 - ((point.value - min) / (max - min)) * 36;
  return <div className="journey-trend">
    <svg viewBox="0 0 280 56" role="img" aria-label={`${label}：${points.length} 个观测，${runs.length} 段可比较记录`} aria-describedby={descriptionID}>
      <line x1="8" y1="48" x2="272" y2="48" className="journey-trend-baseline" />
      {displayed.map((run, index) => <g key={index}>
        {run.length > 1 ? <polyline points={run.map(point => `${x(point).toFixed(2)},${y(point).toFixed(2)}`).join(' ')} fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="3 4" /> : null}
        {run.map(point => <circle key={point.checkpointID} cx={x(point)} cy={y(point)} r="2.6"><title>{observationTime(point.time)} · {point.value} · 快照 #{point.checkpointID}</title></circle>)}
      </g>)}
    </svg>
    {displayedCount < points.length ? <p className="journey-caption">图表抽样显示 {displayedCount}/{points.length} 个实际观测，完整证据可分页查看。</p> : null}
    <p id={descriptionID} className="journey-sr-only">实点表示保存观测，虚线仅连接可比较记录，不代表变化的准确时刻。不同记录段之间不连线。</p>
    <div className={showTable ? 'journey-observations' : 'journey-sr-only'}>
      <table aria-label={`${label}原始观测`}>
        <caption>{label}原始观测</caption>
        <thead><tr><th scope="col">观测时间</th><th scope="col">值</th><th scope="col">快照</th></tr></thead>
        <tbody>{rows.map(({ point, index, pointIndex }) => <tr key={`${index}-${point.checkpointID}`} className={pointIndex === 0 ? 'journey-run-start' : undefined}>
          <td><time dateTime={new Date(point.time).toISOString()}>{observationTime(point.time)}</time><span className="journey-sr-only">，第 {index + 1} 段</span></td><td>{point.value}</td><td>#{point.checkpointID}</td>
        </tr>)}</tbody>
      </table>
      {pageCount > 1 ? <nav aria-label={`${label}观测分页`}>
        <button type="button" disabled={page === 0} onClick={() => setPage(page - 1)}>上一页观测</button>
        <span>第 {page + 1}/{pageCount} 页 · 共 {points.length} 条</span>
        <button type="button" disabled={page === pageCount - 1} onClick={() => setPage(page + 1)}>下一页观测</button>
      </nav> : null}
    </div>
  </div>;
}
