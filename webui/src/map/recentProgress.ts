import type { PlayerProgressResponse, ProgressChange, ProgressCheckpoint } from '../api';
import { progressBoundary, summarizeJourney } from './journeySummary';
import { PROGRESS_METRICS } from './workspaceProgress';

type RecentProgress = { start: number; end: number; changes: ProgressChange[]; checkpoints: ProgressCheckpoint[]; barriers: number[]; partial: boolean };
const date = (time: number) => new Date(time).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
const priority = ['level', 'fast_travel', 'paldeck', 'owned_pals', 'experience', 'capture_total'];

/** Prepare once per response; the playback cursor only selects an already verified interval. */
export function prepareRecentProgress(data: PlayerProgressResponse | undefined, userID: string, start: number, end: number): RecentProgress {
  const out: RecentProgress = { start, end, changes: [], checkpoints: [], barriers: [], partial: false };
  if (!data || data.user_id !== userID || data.status !== 'available' || !Number.isFinite(start) || !Number.isFinite(end) || end <= start) return out;
  const summary = summarizeJourney({ userID, start, end, progress: data });
  out.changes = Object.values(summary.metrics).flatMap(metric => metric?.changes ?? [])
    .sort((a, b) => Date.parse(b.interval_end) - Date.parse(a.interval_end) || priority.indexOf(a.metric) - priority.indexOf(b.metric) || b.id - a.id);
  out.checkpoints = [data.baseline, ...data.checkpoints].filter((cp): cp is ProgressCheckpoint => cp !== null && Number.isFinite(Date.parse(cp.observed_at)))
    .sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at) || a.id - b.id);
  out.barriers = out.checkpoints.filter((cp, i, all) => progressBoundary(all[i - 1], cp)).map(cp => Date.parse(cp.observed_at));
  out.partial = data.checkpoint_total > data.checkpoints.length || data.change_total > data.changes.length;
  return out;
}

export function recentProgressText(data: RecentProgress, cursor: number): string | undefined {
  if (!Number.isFinite(cursor) || cursor < data.start) return;
  const barrier = Math.max(data.start, ...data.barriers.filter(time => time <= cursor));
  const lower = Math.max(barrier, cursor - (data.end - data.start));
  const change = data.changes.find(item => Date.parse(item.interval_end) <= cursor && Date.parse(item.interval_start) >= lower);
  if (!change) return;
  const label = PROGRESS_METRICS.find(metric => metric.key === change.metric)?.label;
  if (!label) return;
  return `最近已知变化${data.partial ? ' · 部分记录' : ''}\n${label} ${change.before.toLocaleString('zh-CN')} → ${change.after.toLocaleString('zh-CN')}\n存档观测区间 ${date(Date.parse(change.interval_start))} — ${date(Date.parse(change.interval_end))}\n未关联此处地点`;
}
