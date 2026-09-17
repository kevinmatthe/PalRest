import { useMemo, useState } from 'react';
import { ArrowUpRight, ChevronDown, Sprout } from 'lucide-react';
import type { PlayerProgressResponse, ProgressChange } from '../api';
import { PROGRESS_METRICS, progressAt, progressMetricValue } from '../map/workspaceProgress';
import { workspaceTime } from './WorkspacePlaybackBar';
type Props = { name: string; data?: PlayerProgressResponse; mode: 'live' | 'history'; cursor: number; loading: boolean; error?: string; onChange: (change: ProgressChange) => void; onRetry: () => void };
const BOUNDARIES: Record<string, string> = { replayed_snapshot: '出现过往存档，重新建立比较基线', after_out_of_order: '观测顺序中断，重新建立比较基线', rollback: '检测到存档回档，已重新建立基线', counter_reset: '计数回退或存档回档，已重新建立基线', inconsistent: '文件一致性未确认，暂停变化比较', after_inconsistent: '一致性恢复，重新建立基线', world_unknown: '世界身份未确认，暂停变化比较', world_changed: '世界已变更，已重新建立基线', schema_changed: '采集口径变更，已重新建立基线', baseline: '首次观测，仅建立基线' };
export function WorkspaceProgress({ name, data, mode, cursor, loading, error, onChange, onRetry }: Props) {
  const [open, setOpen] = useState(true);
  const { checkpoint, changes } = useMemo(() => progressAt(data, Math.min(cursor, Date.now())), [data, cursor]);
  const unattributed = checkpoint?.unattributed_pals;
  const unattributedCount = unattributed?.state === 'known' && Number.isFinite(unattributed.value) ? unattributed.value : undefined;
  const empty = data?.status === 'identity_unknown' ? '玩家身份尚未确认，暂时无法关联存档进度'
    : data?.status === 'not_collected' ? '尚未采集这位玩家的存档进度'
    : !checkpoint ? mode === 'history' ? '此刻之前还没有进度观测' : '尚未采集进度观测' : null;
  return <section className="world-progress" aria-label={`${name}的玩家进度`}>
    <button className="world-progress-heading" type="button" aria-expanded={open} onClick={() => setOpen(value => !value)}>
      <span><Sprout size={17} /><span><strong>旅程进度</strong><small>{name} · {mode === 'history' ? '回放时刻' : '最近存档观测'}</small></span></span><ChevronDown size={16} className={open ? 'is-open' : ''} />
    </button>
    {open ? <div className="world-progress-body">
      {loading ? <p role="status">正在读取旅程进度…</p> : null}
      {error ? <p className="world-progress-warning" role="alert">{error}{data ? ' · 保留上次观测' : ''}<button type="button" onClick={onRetry}>重试</button></p> : null}
      {!loading && empty ? <p>{empty}</p> : null}
      {checkpoint ? <>
        <dl className="world-progress-metrics">{PROGRESS_METRICS.map(metric => <div key={metric.key}><dt>{metric.label}</dt><dd className={checkpoint.metrics?.[metric.key]?.state === 'known' ? 'is-known' : ''}>{progressMetricValue(checkpoint.metrics?.[metric.key])}</dd></div>)}</dl>
        <p>拥有数仅统计已确认个人归属的帕鲁。</p>
        {unattributedCount !== undefined ? unattributedCount > 0 ? <p className="world-progress-warning">另有 {unattributedCount} 只公会帕鲁未确认个人归属，未计入拥有数。</p> : null : <p className="world-progress-warning">个人归属统计尚未完整，公会中未确认归属的帕鲁未计入拥有数。</p>}
        <p className="world-progress-source">来源：存档导入<br />观测 <time dateTime={checkpoint.observed_at}>{workspaceTime(Date.parse(checkpoint.observed_at))}</time><br />采集 <time dateTime={checkpoint.captured_at}>{workspaceTime(Date.parse(checkpoint.captured_at))}</time></p>
        {checkpoint.boundary ? <p className="world-progress-warning">{BOUNDARIES[checkpoint.boundary] ?? '观测边界：数据不可跨边界比较'}</p> : null}
        {!checkpoint.consistent ? <p className="world-progress-warning">文件一致性未确认，此次观测不用于推断变化</p> : null}
      </> : null}
      {data?.status === 'available' ? <>
        <div className="world-progress-section-title"><span>观测到的变化</span><small>{changes.length} 条</small></div>
        {changes.length ? <ol className="world-progress-changes">{changes.map(change => <li key={change.id}><button type="button" onClick={() => onChange(change)}>
          <span><time>{workspaceTime(Date.parse(change.interval_start))} – {workspaceTime(Date.parse(change.interval_end))}</time><ArrowUpRight size={14} /></span>
          <strong>{PROGRESS_METRICS.find(metric => metric.key === change.metric)?.label ?? '进度'} {change.before} → {change.after}<em>{change.delta > 0 ? '+' : ''}{change.delta}</em></strong>
          {(change.added?.length || change.removed?.length) ? <small>新增 {change.added?.length ?? 0} 项 · 移除 {change.removed?.length ?? 0} 项</small> : null}
          <small>存档观测 · 点击回看区间终点</small>
        </button></li>)}</ol> : <p>{checkpoint ? '此刻之前暂无可比较的变化；首次观测仅建立基线。' : '等待更早的进度观测。'}</p>}
        <p className="world-progress-footnote">变化发生于两次观测之间，未关联具体地点。拥有数量变化不等于捕获。</p>
        {data.change_total > data.changes.length ? <p className="world-progress-warning">变化仅加载 {data.changes.length}/{data.change_total} 条，请缩短观察窗口查看更早记录。</p> : null}
        {data.checkpoint_total > data.checkpoints.length ? <p className="world-progress-warning">进度观测仅加载 {data.checkpoints.length}/{data.checkpoint_total} 条，部分时刻可能缺少观测。</p> : null}
      </> : null}
    </div> : null}
  </section>;
}
