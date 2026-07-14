export type ClusterLike = {
  hasLayer: (layer: unknown) => boolean;
  addLayer: (layer: unknown) => void;
  removeLayer: (layer: unknown) => void;
};

export type ExclusionState = {
  excludedKey: string;
};

export function syncFocusClusterExclusion(args: {
  clusterGroup: ClusterLike;
  markersByKey: Map<string, unknown>;
  activeSampleKey: string;
  state: ExclusionState;
}): void {
  const { clusterGroup, markersByKey, activeSampleKey, state } = args;
  const previousKey = state.excludedKey;
  if (previousKey && previousKey !== activeSampleKey) {
    const previous = markersByKey.get(previousKey);
    if (previous && !clusterGroup.hasLayer(previous)) {
      clusterGroup.addLayer(previous);
    }
  }
  if (activeSampleKey) {
    const active = markersByKey.get(activeSampleKey);
    if (active && clusterGroup.hasLayer(active)) {
      clusterGroup.removeLayer(active);
    }
  }
  state.excludedKey = activeSampleKey;
}
