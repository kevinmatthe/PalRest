import { describe, expect, it, vi } from 'vitest';
import { isClusterParentOf, revealFocusMarker } from './mapClusterReveal';

describe('revealFocusMarker', () => {
  it('returns missing when marker is undefined', () => {
    const group = {
      unspiderfy: vi.fn(),
      getVisibleParent: vi.fn(),
    };
    expect(revealFocusMarker(group, undefined)).toBe('missing');
    expect(group.unspiderfy).not.toHaveBeenCalled();
  });

  it('unspiderfies then returns already-visible when parent is the marker itself', () => {
    const marker = { id: 'm' };
    const group = {
      unspiderfy: vi.fn(),
      getVisibleParent: vi.fn(() => marker),
    };
    expect(revealFocusMarker(group, marker)).toBe('already-visible');
    expect(group.unspiderfy).toHaveBeenCalledOnce();
    expect(group.getVisibleParent).toHaveBeenCalledWith(marker);
  });

  it('spiderfies the visible parent cluster when focus is still clustered', () => {
    const marker = { id: 'm' };
    const spiderfy = vi.fn();
    const cluster = { spiderfy };
    const group = {
      unspiderfy: vi.fn(),
      getVisibleParent: vi.fn(() => cluster),
    };
    expect(revealFocusMarker(group, marker)).toBe('spiderfied');
    expect(group.unspiderfy).toHaveBeenCalledOnce();
    expect(spiderfy).toHaveBeenCalledOnce();
  });

  it('does not spiderfy a non-spiderfiable parent', () => {
    const marker = { id: 'm' };
    const group = {
      unspiderfy: vi.fn(),
      getVisibleParent: vi.fn(() => ({ notACluster: true })),
    };
    expect(revealFocusMarker(group, marker)).toBe('already-visible');
  });
});

describe('isClusterParentOf', () => {
  it('is true only when parent is a different spiderfiable layer', () => {
    const marker = { id: 'm' };
    expect(isClusterParentOf({ spiderfy: () => undefined }, marker)).toBe(true);
    expect(isClusterParentOf(marker, marker)).toBe(false);
    expect(isClusterParentOf(null, marker)).toBe(false);
    expect(isClusterParentOf({ x: 1 }, marker)).toBe(false);
  });
});
