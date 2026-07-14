/**
 * Expand a focus sample's cluster by moving its child markers onto a plain
 * layer so they render at exact coordinates — not spiderfy radial offsets.
 */

// Parameter types are `any` so Leaflet MarkerClusterGroup / LayerGroup assign cleanly.
export type ExpandableClusterGroup = {
  getVisibleParent: (marker: any) => any;
  hasLayer: (layer: any) => boolean;
  addLayer: (layer: any) => void;
  removeLayer: (layer: any) => void;
};

export type ExpandableLayer = {
  addLayer: (layer: any) => void;
  removeLayer: (layer: any) => void;
};

export type ClusterWithChildren = {
  getAllChildMarkers: () => unknown[];
};

export type ExpandState = {
  /** Markers currently drawn on the exact-points layer (not in the cluster group). */
  markers: unknown[];
};

export type ExpandResult = 'missing' | 'already-visible' | 'expanded';

function isClusterWithChildren(layer: unknown): layer is ClusterWithChildren {
  return !!layer && typeof (layer as ClusterWithChildren).getAllChildMarkers === 'function';
}

/** Return previously expanded markers to the cluster group. */
export function collapseExpandedMarkers(
  clusterGroup: ExpandableClusterGroup,
  expandedLayer: ExpandableLayer,
  state: ExpandState,
): void {
  for (const marker of state.markers) {
    expandedLayer.removeLayer(marker);
    if (!clusterGroup.hasLayer(marker)) {
      clusterGroup.addLayer(marker);
    }
  }
  state.markers = [];
}

/**
 * If `focusMarker` is still represented by a cluster icon at the current zoom,
 * pull every sibling marker out of the cluster group onto `expandedLayer` so
 * each is painted at its true lat/lng. Previous expansion is collapsed first.
 */
export function expandClusterForExactPoints(
  clusterGroup: ExpandableClusterGroup,
  expandedLayer: ExpandableLayer,
  state: ExpandState,
  focusMarker: unknown | undefined,
): ExpandResult {
  collapseExpandedMarkers(clusterGroup, expandedLayer, state);
  if (!focusMarker) return 'missing';

  const parent = clusterGroup.getVisibleParent(focusMarker);
  if (!parent || parent === focusMarker) return 'already-visible';
  if (!isClusterWithChildren(parent)) return 'already-visible';

  // Snapshot children before any removeLayer (removing mutates the cluster tree).
  const children = parent.getAllChildMarkers();
  for (const child of children) {
    if (clusterGroup.hasLayer(child)) {
      clusterGroup.removeLayer(child);
    }
    expandedLayer.addLayer(child);
    state.markers.push(child);
  }
  return state.markers.length > 0 ? 'expanded' : 'already-visible';
}

/** Drop expansion bookkeeping without re-adding (markers were destroyed/rebuilt). */
export function clearExpandState(state: ExpandState): void {
  state.markers = [];
}
