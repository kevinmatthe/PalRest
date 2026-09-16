import L from 'leaflet';
import { expect, it, vi } from 'vitest';
import { disposeLeafletMap } from './disposeLeafletMap';

it('can leave the map during a zoom without its delayed completion touching a destroyed pane', () => {
  vi.useFakeTimers();
  const host = document.createElement('div'); document.body.append(host);
  const map = L.map(host, { center: [-128, 128], zoom: 2, crs: L.CRS.Simple });
  try {
    // Exercise Leaflet's real CSS-zoom fallback timer, which jsdom cannot trigger by layout.
    (map as L.Map & { _animateZoom: (center: L.LatLng, zoom: number, start: boolean) => void })._animateZoom(L.latLng(-120, 120), 3, true);
    disposeLeafletMap(map);
    expect(() => vi.advanceTimersByTime(1000)).not.toThrow();
    expect(host.querySelector('.leaflet-map-pane')).toBeNull();
  } finally { host.remove(); vi.useRealTimers(); }
});
