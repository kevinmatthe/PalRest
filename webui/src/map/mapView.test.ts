import { describe, expect, it } from 'vitest';
import L from 'leaflet';
import { bearingDeg, shouldPanToFocus } from './mapView';

describe('shouldPanToFocus', () => {
  const bounds = L.latLngBounds(L.latLng(-200, 0), L.latLng(0, 200));

  it('returns false when the point is well inside the viewport', () => {
    expect(shouldPanToFocus(bounds, [-100, 100])).toBe(false);
  });

  it('returns true when the point is outside the viewport', () => {
    expect(shouldPanToFocus(bounds, [-250, 100])).toBe(true);
    expect(shouldPanToFocus(bounds, [-100, 250])).toBe(true);
  });

  it('returns true near the edge so a small inset recenters', () => {
    // y just inside maxY=0 of the raw bounds but outside padded inset
    expect(shouldPanToFocus(bounds, [-5, 100], 0.1)).toBe(true);
  });
});

describe('bearingDeg', () => {
  it('points east when lng increases at fixed lat (CRS.Simple tuple)', () => {
    const deg = bearingDeg([0, 0], [0, 1]);
    expect(deg).toBeCloseTo(90, 5);
  });

  it('points north when lat increases at fixed lng', () => {
    const deg = bearingDeg([0, 0], [1, 0]);
    expect(deg).toBeCloseTo(0, 5);
  });

  it('points south when lat decreases', () => {
    const deg = bearingDeg([1, 0], [0, 0]);
    expect(deg).toBeCloseTo(180, 5);
  });
});
