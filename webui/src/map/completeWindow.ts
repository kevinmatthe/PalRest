import { getPlayerProgress, getPlayerTimeline, type PlayerProgressResponse, type PlayerTimelineResponse } from '../api';

// Bound work without ever presenting a limited suffix as a complete window.
const MAX_REQUESTS = 511;
const MAX_ROWS = 100_000;
async function completeParts<T extends { user_id: string }>(userID: string, start: number, end: number, signal: AbortSignal,
  request: (from: string, to: string) => Promise<T>, counts: (data: T) => [number, number][]) {
  if (!Number.isFinite(start) || !Number.isFinite(end) || start >= end || end - start > 31 * 86400000) throw new Error('观察窗口无效');
  const parts: T[] = [];
  let root: T | undefined, requests = 0, rows = 0;
  async function load(from: number, to: number): Promise<void> {
    signal.throwIfAborted();
    if (++requests > MAX_REQUESTS) throw new Error('窗口记录过多，无法完整加载，请缩小观察范围');
    const data = await request(new Date(from).toISOString(), new Date(to).toISOString());
    signal.throwIfAborted();
    if (data.user_id !== userID) throw new Error('窗口响应玩家不匹配');
    root ??= data;
    const sizes = counts(data);
    rows += sizes.reduce((sum, [loaded]) => sum + loaded, 0);
    if (rows > MAX_ROWS) throw new Error('窗口记录过多，无法完整加载，请缩小观察范围');
    if (sizes.every(([loaded, total]) => loaded >= total)) { parts.push(data); return; }
    if (to - from <= 1) throw new Error('同一时刻记录过多，无法完整加载');
    const middle = Math.floor((from + to) / 2);
    // Split ranges, not rounded row timestamps: the server stores nanosecond precision.
    await load(from, middle);
    await load(middle, to);
  }
  await load(start, end);
  return { root: root!, parts };
}
function unique<T>(rows: T[], identity: (row: T) => string | number): T[] {
  return [...new Map(rows.map(row => [identity(row), row])).values()];
}

export async function loadProgressWindow(userID: string, start: number, end: number, signal: AbortSignal): Promise<PlayerProgressResponse> {
  const { root, parts } = await completeParts(userID, start, end, signal,
    (from, to) => getPlayerProgress(userID, from, to, 500, signal),
    data => [[data.checkpoints.length, data.checkpoint_total], [data.changes.length, data.change_total]]);
  if (parts.some(data => data.status === 'identity_unknown') && root.status !== 'identity_unknown') throw new Error('加载期间玩家身份发生变化，请重试');
  const checkpoints = unique(parts.flatMap(data => data.checkpoints), row => row.id).sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at) || a.id - b.id);
  const changes = unique(parts.flatMap(data => data.changes), row => row.id).sort((a, b) => Date.parse(b.interval_end) - Date.parse(a.interval_end) || b.id - a.id);
  if (checkpoints.length !== root.checkpoint_total || changes.length !== root.change_total) throw new Error('加载期间进度记录发生变化，无法确认完整窗口，请重试');
  return { ...root, checkpoints, changes };
}

export async function loadTimelineWindow(userID: string, start: number, end: number, signal: AbortSignal): Promise<PlayerTimelineResponse> {
  const { root, parts } = await completeParts(userID, start, end, signal,
    (from, to) => getPlayerTimeline(userID, from, to, 500, signal),
    data => [[data.trajectories.length, data.trajectory_total ?? data.trajectories.length], [data.events.length, data.event_total ?? data.events.length], [data.private_samples.length, data.private_sample_total ?? data.private_samples.length]]);
  const identity = (row: { user_id: string; observed_at: string; source_ref: string }) => JSON.stringify([row.user_id, row.observed_at, row.source_ref]);
  const trajectories = unique(parts.flatMap(data => data.trajectories), identity).sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at));
  const events = unique(parts.flatMap(data => data.events), row => row.id).sort((a, b) => Date.parse(a.occurred_at) - Date.parse(b.occurred_at));
  const private_samples = unique(parts.flatMap(data => data.private_samples), identity).sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at));
  if (trajectories.length !== (root.trajectory_total ?? trajectories.length) || events.length !== (root.event_total ?? events.length) || private_samples.length !== (root.private_sample_total ?? private_samples.length)) throw new Error('加载期间轨迹记录发生变化，无法确认完整窗口，请重试');
  return { ...root, trajectories, events, private_samples, trajectory_total: trajectories.length, event_total: events.length, private_sample_total: private_samples.length };
}
