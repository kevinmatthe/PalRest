import { describe, expect, it } from 'vitest';
import {
  syncFocusClusterExclusion,
  type ClusterLike,
  type ExclusionState,
} from './mapClusterExclusion';

function fakeCluster(initialKeys: string[]): {
  group: ClusterLike;
  layers: Map<string, object>;
} {
  const layers = new Map<string, object>();
  for (const k of initialKeys) layers.set(k, { key: k });
  const group: ClusterLike = {
    hasLayer: (layer) => [...layers.values()].includes(layer as object),
    addLayer: (layer) => {
      const key = (layer as { key?: string }).key;
      if (key) layers.set(key, layer as object);
    },
    removeLayer: (layer) => {
      for (const [k, v] of layers) {
        if (v === layer) layers.delete(k);
      }
    },
  };
  return { group, layers };
}

describe('syncFocusClusterExclusion', () => {
  it('removes active key from cluster and records exclusion', () => {
    const { group, layers } = fakeCluster(['a', 'b']);
    const markersByKey = new Map<string, unknown>();
    for (const [k, v] of layers) markersByKey.set(k, v);
    const state: ExclusionState = { excludedKey: '' };

    syncFocusClusterExclusion({
      clusterGroup: group,
      markersByKey,
      activeSampleKey: 'b',
      state,
    });

    expect(state.excludedKey).toBe('b');
    expect(layers.has('b')).toBe(false);
    expect(layers.has('a')).toBe(true);
  });

  it('re-adds previous excluded when key changes', () => {
    const { group, layers } = fakeCluster(['a']);
    const markersByKey = new Map<string, unknown>();
    const markerA = { key: 'a' };
    const markerB = { key: 'b' };
    markersByKey.set('a', markerA);
    markersByKey.set('b', markerB);
    layers.set('a', markerA);
    // b not in cluster (simulating previous exclude of b)
    const state: ExclusionState = { excludedKey: 'b' };

    syncFocusClusterExclusion({
      clusterGroup: group,
      markersByKey,
      activeSampleKey: 'a',
      state,
    });

    expect(state.excludedKey).toBe('a');
    expect(layers.has('b')).toBe(true); // previous re-added
    expect(layers.has('a')).toBe(false);
  });

  it('re-excludes same key after rebuild (excludedKey cleared, all markers back in cluster)', () => {
    const { group, layers } = fakeCluster(['focus', 'other']);
    const markersByKey = new Map<string, unknown>([...layers.entries()]);
    // rebuild cleared exclusion ref; focus is back in the cluster
    const state: ExclusionState = { excludedKey: '' };

    syncFocusClusterExclusion({
      clusterGroup: group,
      markersByKey,
      activeSampleKey: 'focus',
      state,
    });

    expect(layers.has('focus')).toBe(false);
    expect(layers.has('other')).toBe(true);
    expect(state.excludedKey).toBe('focus');
  });
});
