import L from 'leaflet';
import { expect, it, vi } from 'vitest';
import type { JourneyHeatCell } from './journeyTypes';
import { WorldMapHeat } from './worldMapHeat';

const cell: JourneyHeatCell = { id: '1:2', x: 15000, y: 25000, durationMs: 60000, start: 1000, end: 61000, edges: [] };
it('reuses weighted markers and exposes only text with interval evidence', () => {
  const layer = L.layerGroup();
  const onSelect = vi.fn();
  const heat = new WorldMapHeat(layer, onSelect);
  heat.sync([cell]);
  const first = layer.getLayers()[0] as L.CircleMarker;
  const radius = first.getRadius();
  const next = { ...cell, durationMs: 120000, end: 121000 };
  heat.sync([next]);
  expect(layer.getLayers()[0]).toBe(first);
  expect(first.getRadius()).toBeGreaterThan(radius);
  const content = first.getTooltip()?.getContent() as HTMLElement;
  expect(content.textContent).toContain('120 秒');
  expect(content.textContent).toContain('近似');
  first.fire('click');
  expect(onSelect).toHaveBeenCalledWith(next);
  heat.sync([]);
  expect(layer.getLayers()).toHaveLength(0);
});

it('ignores invalid and zero duration data, and clear removes every marker', () => {
  const layer = L.layerGroup();
  const heat = new WorldMapHeat(layer, () => {});
  heat.sync([cell, { ...cell, id: 'invalid', x: NaN }, { ...cell, id: 'zero', durationMs: 0 }]);
  expect(layer.getLayers()).toHaveLength(1);
  heat.clear();
  expect(layer.getLayers()).toHaveLength(0);
});
