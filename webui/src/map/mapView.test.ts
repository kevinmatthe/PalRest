import { describe, expect, it } from 'vitest';
import L from 'leaflet';
import { shouldPanToFocus } from './mapView';

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
