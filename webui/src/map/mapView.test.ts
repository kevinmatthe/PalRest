import { describe, expect, it } from 'vitest';
import L from 'leaflet';
import { bearingDeg, midpointLatLng, shouldPanToFocus, travelBearingEndpoints } from './mapView';

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

describe('travelBearingEndpoints', () => {
  it('prefers prev→focus over focus→next', () => {
    expect(travelBearingEndpoints([0, 0], [1, 0], [2, 0])).toEqual({ from: [0, 0], to: [1, 0] });
  });

  it('falls back to focus→next when prev is missing', () => {
    expect(travelBearingEndpoints(undefined, [1, 0], [2, 0])).toEqual({ from: [1, 0], to: [2, 0] });
  });

  it('returns undefined with no neighbors', () => {
    expect(travelBearingEndpoints(undefined, [1, 0], undefined)).toBeUndefined();
  });
});

describe('midpointLatLng', () => {
  it('averages CRS.Simple tuples', () => {
    expect(midpointLatLng([0, 0], [2, 4])).toEqual([1, 2]);
  });
});
