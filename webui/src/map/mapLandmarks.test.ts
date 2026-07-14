import { describe, expect, it } from 'vitest';
import {
  knownMapLocation,
  MAP_LANDMARKS,
  nearestLandmark,
  type MapLandmark,
} from './mapLandmarks';

const fixtures: MapLandmark[] = [
  { id: 'ft-1', nameZh: '初始高地', x: 0, y: 0, kind: 'fast_travel' },
  { id: 'ft-2', nameZh: '小桥瀑布', x: 100_000, y: 0, kind: 'fast_travel' },
  { id: 'tw-1', nameZh: '青岚之塔', x: 50_000, y: 50_000, kind: 'boss_tower' },
];

describe('nearestLandmark', () => {
  it('hits within radius', () => {
    const hit = nearestLandmark({ x: 500, y: -200 }, fixtures, 25_000);
    expect(hit?.nameZh).toBe('初始高地');
  });

  it('misses outside radius', () => {
    expect(nearestLandmark({ x: 40_000, y: 0 }, fixtures, 25_000)).toBeUndefined();
  });

  it('tie-breaks by smaller id', () => {
    const twins: MapLandmark[] = [
      { id: 'b', nameZh: '乙', x: 0, y: 0, kind: 'fast_travel' },
      { id: 'a', nameZh: '甲', x: 0, y: 0, kind: 'fast_travel' },
    ];
    expect(nearestLandmark({ x: 0, y: 0 }, twins, 25_000)?.id).toBe('a');
  });
});

describe('knownMapLocation', () => {
  it('returns 靠近 label or empty', () => {
    expect(knownMapLocation({ x: 0, y: 0 }, fixtures)).toBe('靠近 · 初始高地');
    expect(knownMapLocation({ x: 9e9, y: 9e9 }, fixtures)).toBe('');
  });
});

describe('MAP_LANDMARKS', () => {
  it('ships full reference coordinate set', () => {
    expect(MAP_LANDMARKS.length).toBeGreaterThanOrEqual(146);
  });
});
