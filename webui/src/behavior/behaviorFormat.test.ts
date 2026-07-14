import { describe, expect, it } from 'vitest';
import {
  BEHAVIOR_CLASS_LABELS,
  formatBehaviorDistance,
  formatBehaviorShare,
  formatBehaviorSpeed,
  formatDensityPerHour,
  formatDominantLabel,
} from './behaviorFormat';

describe('BEHAVIOR_CLASS_LABELS', () => {
  it('maps class keys to Chinese labels', () => {
    expect(BEHAVIOR_CLASS_LABELS.traveling).toBe('跑图');
    expect(BEHAVIOR_CLASS_LABELS.local).toBe('局部');
    expect(BEHAVIOR_CLASS_LABELS.stationary).toBe('挂机');
  });
});

describe('formatBehaviorShare', () => {
  it('formats fractional shares as percentages', () => {
    expect(formatBehaviorShare(0.5)).toBe('50%');
    expect(formatBehaviorShare(0.333)).toBe('33%');
    expect(formatBehaviorShare(1)).toBe('100%');
  });

  it('returns 0% for non-positive or non-finite values', () => {
    expect(formatBehaviorShare(0)).toBe('0%');
    expect(formatBehaviorShare(-0.1)).toBe('0%');
    expect(formatBehaviorShare(NaN)).toBe('0%');
    expect(formatBehaviorShare(Infinity)).toBe('0%');
  });
});

describe('formatBehaviorDistance', () => {
  it('rounds and formats with zh-CN locale and unit', () => {
    expect(formatBehaviorDistance(1234.6)).toBe(`${Math.round(1234.6).toLocaleString('zh-CN')} 世界坐标`);
    expect(formatBehaviorDistance(0)).toBe(`${(0).toLocaleString('zh-CN')} 世界坐标`);
  });

  it('returns dash for non-finite values', () => {
    expect(formatBehaviorDistance(NaN)).toBe('-');
    expect(formatBehaviorDistance(Infinity)).toBe('-');
  });
});

describe('formatBehaviorSpeed', () => {
  it('keeps one decimal below 100 and rounds integers at or above 100', () => {
    expect(formatBehaviorSpeed(12.34)).toBe(`${(12.3).toLocaleString('zh-CN')} 坐标/秒`);
    expect(formatBehaviorSpeed(99.96)).toBe(`${(100).toLocaleString('zh-CN')} 坐标/秒`);
    expect(formatBehaviorSpeed(150.4)).toBe(`${(150).toLocaleString('zh-CN')} 坐标/秒`);
  });

  it('returns dash for non-finite values', () => {
    expect(formatBehaviorSpeed(NaN)).toBe('-');
    expect(formatBehaviorSpeed(Infinity)).toBe('-');
  });
});

describe('formatDominantLabel', () => {
  it('maps known classes and unknown', () => {
    expect(formatDominantLabel('traveling')).toBe('跑图');
    expect(formatDominantLabel('local')).toBe('局部');
    expect(formatDominantLabel('stationary')).toBe('挂机');
    expect(formatDominantLabel('unknown')).toBe('未知');
  });
});

describe('formatDensityPerHour', () => {
  it('rounds below 10 to one decimal and at/above 10 to integer', () => {
    expect(formatDensityPerHour(3.26)).toBe('3.3 点/时');
    expect(formatDensityPerHour(9.94)).toBe('9.9 点/时');
    expect(formatDensityPerHour(12.4)).toBe('12 点/时');
  });

  it('returns 0 点/时 for non-positive or non-finite values', () => {
    expect(formatDensityPerHour(0)).toBe('0 点/时');
    expect(formatDensityPerHour(-1)).toBe('0 点/时');
    expect(formatDensityPerHour(NaN)).toBe('0 点/时');
    expect(formatDensityPerHour(Infinity)).toBe('0 点/时');
  });
});
