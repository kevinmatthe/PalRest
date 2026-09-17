import type L from 'leaflet';

export function disposeLeafletMap(map: L.Map) {
  // Leaflet 1.9.4's CSS zoom schedules an untracked 250ms completion timer.
  // remove() stops pan/fly but deletes _mapPane without clearing this latch;
  // the timer then tries to move a destroyed map. Keep the workaround isolated
  // and covered by an actual Leaflet test when updating the dependency.
  (map as L.Map & { _animatingZoom?: boolean })._animatingZoom = false;
  map.remove();
}
