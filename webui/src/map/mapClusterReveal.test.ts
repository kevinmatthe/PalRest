import { describe, expect, it, vi } from 'vitest';
import {
  clearExpandState,
  collapseExpandedMarkers,
  expandClusterForExactPoints,
  type ExpandState,
} from './mapClusterReveal';

function fakeClusterEnv(initialKeys: string[]) {
  const clusterLayers = new Map<string, object>();
  const expandedLayers = new Map<string, object>();
  const markers = new Map<string, object>();
  for (const key of initialKeys) {
    const m = { key };
    markers.set(key, m);
    clusterLayers.set(key, m);
  }

  const clusterGroup = {
    hasLayer: (layer: unknown) => [...clusterLayers.values()].includes(layer as object),
    addLayer: (layer: unknown) => {
      const key = (layer as { key?: string }).key;
      if (key) clusterLayers.set(key, layer as object);
    },
    removeLayer: (layer: unknown) => {
      for (const [k, v] of clusterLayers) {
        if (v === layer) clusterLayers.delete(k);
      }
    },
    getVisibleParent: (marker: unknown) => {
      // All markers in clusterLayers share one synthetic parent cluster.
      if (clusterLayers.size === 0) return marker;
      if ([...clusterLayers.values()].includes(marker as object) && clusterLayers.size > 1) {
        return parentCluster;
      }
      return marker;
    },
  };

  const parentCluster = {
    getAllChildMarkers: () => [...clusterLayers.values()],
  };

  const expandedLayer = {
    addLayer: (layer: unknown) => {
      const key = (layer as { key?: string }).key;
      if (key) expandedLayers.set(key, layer as object);
    },
    removeLayer: (layer: unknown) => {
      for (const [k, v] of expandedLayers) {
        if (v === layer) expandedLayers.delete(k);
      }
    },
  };

  return { clusterGroup, expandedLayer, markers, clusterLayers, expandedLayers, parentCluster };
}

describe('expandClusterForExactPoints', () => {
  it('returns missing when focus marker is absent', () => {
    const { clusterGroup, expandedLayer } = fakeClusterEnv(['a']);
    const state: ExpandState = { markers: [] };
    expect(expandClusterForExactPoints(clusterGroup, expandedLayer, state, undefined)).toBe('missing');
  });

  it('returns already-visible when the marker is not under a cluster parent', () => {
    const { clusterGroup, expandedLayer, markers } = fakeClusterEnv(['solo']);
    // single marker: getVisibleParent returns the marker itself
    const state: ExpandState = { markers: [] };
    expect(expandClusterForExactPoints(clusterGroup, expandedLayer, state, markers.get('solo'))).toBe(
      'already-visible',
    );
    expect(state.markers).toHaveLength(0);
  });

  it('moves all siblings out of the cluster onto the exact layer at true positions', () => {
    const { clusterGroup, expandedLayer, markers, clusterLayers, expandedLayers } = fakeClusterEnv([
      'a',
      'b',
      'c',
    ]);
    const state: ExpandState = { markers: [] };
    const result = expandClusterForExactPoints(clusterGroup, expandedLayer, state, markers.get('b'));
    expect(result).toBe('expanded');
    expect(state.markers).toHaveLength(3);
    expect(clusterLayers.size).toBe(0);
    expect(expandedLayers.size).toBe(3);
    expect(expandedLayers.has('a')).toBe(true);
    expect(expandedLayers.has('b')).toBe(true);
    expect(expandedLayers.has('c')).toBe(true);
  });

  it('collapses the previous expansion before expanding a new focus', () => {
    const env = fakeClusterEnv(['a', 'b', 'c', 'd']);
    // Simulate two clusters by customizing getVisibleParent after first expand.
    const state: ExpandState = { markers: [] };
    expandClusterForExactPoints(env.clusterGroup, env.expandedLayer, state, env.markers.get('a'));
    expect(state.markers).toHaveLength(4);

    // Put only d back as a lone "already visible" case after collapse via second call with no cluster
    // First collapse happens inside expand — all return to cluster then expand again.
    const result = expandClusterForExactPoints(
      env.clusterGroup,
      env.expandedLayer,
      state,
      env.markers.get('c'),
    );
    expect(result).toBe('expanded');
    // Still one synthetic parent for all keys still in cluster after collapse-then-reexpand
    expect(state.markers.length).toBeGreaterThan(0);
    // Nothing left both in cluster and expanded inconsistently for expanded set
    for (const m of state.markers) {
      expect(env.clusterGroup.hasLayer(m)).toBe(false);
    }
  });
});

describe('collapseExpandedMarkers', () => {
  it('returns markers to the cluster group and clears state', () => {
    const { clusterGroup, expandedLayer, markers, clusterLayers, expandedLayers } = fakeClusterEnv([
      'a',
      'b',
    ]);
    const state: ExpandState = { markers: [] };
    expandClusterForExactPoints(clusterGroup, expandedLayer, state, markers.get('a'));
    collapseExpandedMarkers(clusterGroup, expandedLayer, state);
    expect(state.markers).toHaveLength(0);
    expect(expandedLayers.size).toBe(0);
    expect(clusterLayers.size).toBe(2);
  });
});

describe('clearExpandState', () => {
  it('drops bookkeeping without re-adding', () => {
    const state: ExpandState = { markers: [{ id: 1 }, { id: 2 }] };
    clearExpandState(state);
    expect(state.markers).toEqual([]);
  });
});
