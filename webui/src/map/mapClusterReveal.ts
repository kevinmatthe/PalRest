/** Minimal surface for revealing a focus sample inside leaflet.markercluster. */
export type RevealClusterGroup = {
  unspiderfy: () => void;
  getVisibleParent: (marker: unknown) => unknown;
};

export type Spiderfiable = {
  spiderfy: () => void;
};

export type RevealResult = 'missing' | 'already-visible' | 'spiderfied';

function isSpiderfiable(layer: unknown): layer is Spiderfiable {
  return !!layer && typeof (layer as Spiderfiable).spiderfy === 'function';
}

/**
 * Unspiderfy any open cluster, then spiderfy the visible parent of `marker`
 * when it is still clustered at the current zoom.
 * Keeps the marker in the cluster group (do not remove it first).
 */
export function revealFocusMarker(
  clusterGroup: RevealClusterGroup,
  marker: unknown | undefined,
): RevealResult {
  if (!marker) return 'missing';
  clusterGroup.unspiderfy();
  const parent = clusterGroup.getVisibleParent(marker);
  if (!parent || parent === marker) return 'already-visible';
  if (isSpiderfiable(parent)) {
    parent.spiderfy();
    return 'spiderfied';
  }
  return 'already-visible';
}

/** True when a cluster icon currently stands in for the focus marker. */
export function isClusterParentOf(parent: unknown, marker: unknown): boolean {
  return !!parent && !!marker && parent !== marker && isSpiderfiable(parent);
}
