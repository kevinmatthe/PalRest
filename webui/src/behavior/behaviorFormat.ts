import type { BehaviorDominantClass } from './behaviorTypes';

export const BEHAVIOR_CLASS_LABELS = {
  traveling: '跑图',
  local: '局部',
  stationary: '挂机',
} as const;

export function formatBehaviorShare(share: number): string {
  if (!Number.isFinite(share) || share <= 0) return '0%';
  return `${Math.round(share * 100)}%`;
}

export function formatBehaviorDistance(value: number): string {
  if (!Number.isFinite(value)) return '-';
  return `${Math.round(value).toLocaleString('zh-CN')} 世界坐标`;
}

export function formatBehaviorSpeed(value: number): string {
  if (!Number.isFinite(value)) return '-';
  const rounded = value >= 100 ? Math.round(value) : Math.round(value * 10) / 10;
  return `${rounded.toLocaleString('zh-CN')} 坐标/秒`;
}

export function formatDominantLabel(value: BehaviorDominantClass): string {
  if (value === 'unknown') return '未知';
  return BEHAVIOR_CLASS_LABELS[value];
}

export function formatDensityPerHour(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 点/时';
  const rounded = value >= 10 ? Math.round(value) : Math.round(value * 10) / 10;
  return `${rounded} 点/时`;
}
