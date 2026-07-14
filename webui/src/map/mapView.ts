import L from 'leaflet';

/** True when the focus point is outside a slightly inset viewport (so near-edge still recenters). */
export function shouldPanToFocus(
  bounds: L.LatLngBounds,
  point: L.LatLngExpression,
  insetRatio = 0.08,
): boolean {
  const latLng = L.latLng(point as L.LatLngTuple);
  const inset = Math.min(0.45, Math.max(0, insetRatio));
  return !bounds.pad(-inset).contains(latLng);
}
