import type { PlayerTimelineResponse, TimelineEvent } from '../api';
import { knownMapLocation as resolveLandmark } from '../map/mapLandmarks';

export type LogItem =
  | { kind: 'event'; at: string; key: string; item: TimelineEvent }
  | { kind: 'trajectory'; at: string; key: string; item: PlayerTimelineResponse['trajectories'][number] }
  | { kind: 'private'; at: string; key: string; item: PlayerTimelineResponse['private_samples'][number] };

export type TrajectorySample = PlayerTimelineResponse['trajectories'][number];

export const LOCATION_COORDINATE_FALLBACK = '地图坐标位置';

// Same Leaflet CRS.Simple coordinate transform used by zaigie/palworld-server-tool.
// LAND_SCAPE order in the reference repo is [maxX, maxY, minX, minY].
export const PALWORLD_LANDSCAPE = [349400, 724400, -1099400, -724400] as const;
export const PALWORLD_TILE_URL = '/map/tiles/{z}/{x}/{y}.png';
export const PALWORLD_TILE_FALLBACK_URL = 'https://palworld.gg/images/tiles/{z}/{x}/{y}.png';
export const PALWORLD_TILE_BOUNDS: [[number, number], [number, number]] = [[0, 0], [-256, 256]];

export const KNOWN_EVENTS = new Set([
  'player_joined', 'player_left', 'player_attribute_changed',
  'guard_warning_attempted', 'guard_warning_delivered', 'guard_warning_failed',
  'enforcement_attempted', 'enforcement_succeeded', 'enforcement_failed',
]);

export const EVENT_LABELS: Record<string, string> = {
  player_joined: '玩家加入',
  player_left: '玩家离开',
  player_attribute_changed: '玩家属性变更',
  guard_warning_attempted: '尝试发送提醒',
  guard_warning_delivered: '提醒已送达',
  guard_warning_failed: '提醒发送失败',
  enforcement_attempted: '尝试执行限制',
  enforcement_succeeded: '限制执行成功',
  enforcement_failed: '限制执行失败',
};

export const CONFIDENCE_LABELS: Record<string, string> = {
  observed: '实测',
  snapshot_derived: '存档推导',
};

export function trajectoryKey(sample: TrajectorySample) {
  return `${sample.user_id}:${sample.segment_id}:${sample.observed_at}:${sample.source_ref}`;
}

export function trajectorySamples(items: LogItem[]) {
  return items.filter((item): item is Extract<LogItem, { kind: 'trajectory' }> => item.kind === 'trajectory').map((item) => item.item);
}

export function projectWorldXY(worldX: number, worldY: number): [number, number] {
  const [maxX, maxY, minX, minY] = PALWORLD_LANDSCAPE;
  if (worldX >= -256 && worldX <= 256) return [worldX, worldY];
  const x = -256 + (256 * (worldX - minX)) / (maxX - minX);
  const y = (256 * (worldY - minY)) / (maxY - minY);
  return [x, y];
}

export function projectWorldSample(sample: TrajectorySample): [number, number] {
  return projectWorldXY(sample.x, sample.y);
}

export function latestPointAt(samples: TrajectorySample[], activeAt: string | undefined) {
  if (!samples.length) return undefined;
  const activeMS = activeAt ? Date.parse(activeAt) : Number.NaN;
  if (!Number.isFinite(activeMS)) return samples[0];
  return [...samples].reverse().find((sample) => Date.parse(sample.observed_at) <= activeMS) ?? samples[0];
}

export function formatTimelineDateTime(value: string | undefined): string {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date);
}

export function eventLabel(eventType: string, known = KNOWN_EVENTS.has(eventType)) {
  return known ? EVENT_LABELS[eventType] ?? eventType : '未知事件';
}

export function confidenceLabel(value: string) {
  return CONFIDENCE_LABELS[value] ?? value;
}

export function mapLocationLabel(sample: TrajectorySample) {
  return resolveLandmark(sample) || LOCATION_COORDINATE_FALLBACK;
}
