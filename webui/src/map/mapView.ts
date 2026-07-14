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

/**
 * CSS rotation degrees clockwise from up for CRS.Simple LatLngExpression.
 * Uses atan2(Δlng, Δlat) so north (+lat) → 0°, east (+lng) → 90°.
 */
export function bearingDeg(from: L.LatLngExpression, to: L.LatLngExpression): number {
  const a = L.latLng(from as L.LatLngTuple);
  const b = L.latLng(to as L.LatLngTuple);
  return (Math.atan2(b.lng - a.lng, b.lat - a.lat) * 180) / Math.PI;
}

/** Midpoint in CRS.Simple lat/lng space (for mid-edge direction markers). */
export function midpointLatLng(from: L.LatLngExpression, to: L.LatLngExpression): L.LatLngTuple {
  const a = L.latLng(from as L.LatLngTuple);
  const b = L.latLng(to as L.LatLngTuple);
  return [(a.lat + b.lat) / 2, (a.lng + b.lng) / 2];
}

/**
 * Travel direction for the focus sample: prefer prev→focus (arriving),
 * else focus→next (leaving). Returns undefined when no neighbor exists.
 */
export function travelBearingEndpoints(
  prev: L.LatLngExpression | undefined,
  focus: L.LatLngExpression,
  next: L.LatLngExpression | undefined,
): { from: L.LatLngExpression; to: L.LatLngExpression } | undefined {
  if (prev) return { from: prev, to: focus };
  if (next) return { from: focus, to: next };
  return undefined;
}
