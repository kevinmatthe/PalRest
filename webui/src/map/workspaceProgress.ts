import type { PlayerProgressResponse, ProgressCheckpoint, ProgressMetric, ProgressMetricName } from '../api';
export const PROGRESS_METRICS: { key: ProgressMetricName; label: string }[] = [
  { key: 'owned_pals', label: '拥有帕鲁' }, { key: 'capture_total', label: '累计捕获记录' },
  { key: 'paldeck', label: '图鉴解锁' }, { key: 'fast_travel', label: '传送点解锁' },
];
export function progressMetricValue(metric?: ProgressMetric): string {
  if (metric?.state === 'unsupported') return '暂不支持';
  return metric?.state === 'known' && Number.isFinite(metric.value) ? String(metric.value) : '未采集';
}
export function progressAt(data: PlayerProgressResponse | undefined, cursor: number) {
  let checkpoint: ProgressCheckpoint | null = null;
  if (data?.status === 'available') {
    for (const item of [data.baseline, ...data.checkpoints]) {
      if (item && Date.parse(item.observed_at) <= cursor && (!checkpoint || Date.parse(item.observed_at) >= Date.parse(checkpoint.observed_at))) checkpoint = item;
    }
  }
  return { checkpoint, changes: data?.status === 'available' ? data.changes.filter(change => Date.parse(change.interval_end) <= cursor) : [] };
}
export function progressBounds(data?: PlayerProgressResponse) {
  const times = data?.status === 'available' ? [
    ...data.checkpoints.map(item => Date.parse(item.observed_at)),
    ...data.changes.flatMap(item => [Date.parse(item.interval_start), Date.parse(item.interval_end)]),
  ].filter(Number.isFinite) : [];
  return { start: Math.min(...times), end: Math.max(...times) };
}
