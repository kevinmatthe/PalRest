import L from 'leaflet';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { WorldMapMarkers, projectWorkspaceXY, type MapDisplayPoint } from './worldMapMarkers';

let map: L.Map;
let host: HTMLDivElement;
let markers: WorldMapMarkers;
const p: MapDisplayPoint = { user_id: 'u', name: '<img src=x onerror=alert(1)>', x: -200000, y: 100000, observedAt: '2026-09-16T10:00:00Z', continuity: 'live', state: '当前位置' };
beforeEach(() => {
  vi.useFakeTimers();
  host = document.createElement('div'); document.body.append(host);
  map = L.map(host, { center: [-128, 128], zoom: 2, crs: L.CRS.Simple, zoomAnimation: false });
  markers = new WorldMapMarkers(map, () => {});
});
afterEach(() => { markers.clear(); map.remove(); host.remove(); vi.restoreAllMocks(); vi.useRealTimers(); });

it('keeps marker and tooltip DOM stable during polling and treats player names as text', () => {
  markers.sync([p], 'u', 'live', false, false);
  const icon = host.querySelector('.world-player-pin');
  const card = host.querySelector('.world-marker-card');
  expect(card?.textContent).toContain(p.name);
  expect(card?.querySelector('img')).toBeNull();
  markers.sync([{ ...p, x: p.x + 1000, observedAt: '2026-09-16T10:00:30Z' }], 'u', 'live', false, false);
  vi.advanceTimersByTime(600);
  expect(host.querySelector('.world-player-pin')).toBe(icon);
  expect(host.querySelector('.world-marker-card')).toBe(card);
});

it('moves through intermediate coordinates and cancels live animation on history switch', () => {
  markers.sync([p], 'u', 'live', false, false);
  const spy = vi.spyOn(L.Marker.prototype, 'setLatLng');
  const next = { ...p, x: p.x + 10000, observedAt: '2026-09-16T10:00:30Z' };
  markers.sync([next], 'u', 'live', false, false);
  vi.advanceTimersByTime(240);
  const middle = L.latLng(spy.mock.calls.at(-1)![0]);
  expect(middle.lat).toBeGreaterThan(projectWorkspaceXY(p.x, p.y)[0]);
  expect(middle.lat).toBeLessThan(projectWorkspaceXY(next.x, next.y)[0]);
  markers.sync([{ ...next, continuity: 'history:s1', state: '回放位置' }], 'u', 'history', false, false);
  spy.mockClear();
  vi.advanceTimersByTime(1000);
  expect(spy).not.toHaveBeenCalled();
});

it('does not animate suspected teleports or reduced-motion updates', () => {
  markers.sync([p], 'u', 'live', false, false);
  const spy = vi.spyOn(L.Marker.prototype, 'setLatLng');
  const next = { ...p, x: p.x + 100000, observedAt: '2026-09-16T10:00:30Z' };
  markers.sync([next], 'u', 'live', false, false);
  expect(L.latLng(spy.mock.calls.at(-1)![0]).lat).toBe(projectWorkspaceXY(next.x, next.y)[0]);
  spy.mockClear(); vi.advanceTimersByTime(600); expect(spy).not.toHaveBeenCalled();
  markers.sync([{ ...next, x: next.x + 1000, observedAt: '2026-09-16T10:01:00Z' }], 'u', 'live', true, false);
  spy.mockClear(); vi.advanceTimersByTime(600); expect(spy).not.toHaveBeenCalled();
});

it('projects real world coordinates continuously through zero', () => {
  const a = projectWorkspaceXY(-1, 1000), b = projectWorkspaceXY(1, 1000);
  expect(Math.abs(a[0] - b[0])).toBeLessThan(.001);
});

it('updates selected-player progress as safe text without moving or replacing the marker', () => {
  markers.sync([{ ...p, recentProgress: '拥有帕鲁 5 → 7\n<img src=x onerror=alert(1)>' }], 'u', 'live', false, false);
  const icon = host.querySelector('.world-player-pin');
  const card = host.querySelector('.world-marker-card');
  expect(card?.textContent).toContain('拥有帕鲁 5 → 7');
  expect(card?.querySelector('img')).toBeNull();
  const move = vi.spyOn(L.Marker.prototype, 'setLatLng');
  markers.sync([{ ...p, recentProgress: '存档等级 10 → 11' }], 'u', 'live', false, false);
  expect(host.querySelector('.world-player-pin')).toBe(icon);
  expect(host.querySelector('.world-marker-card')).toBe(card);
  expect(card?.textContent).toContain('存档等级 10 → 11');
  expect(move).not.toHaveBeenCalled();
  markers.sync([p], 'u', 'history', false, false);
  expect(card?.textContent).not.toContain('存档等级');
  markers.sync([{ ...p, recentProgress: '不能留在旧玩家上' }], 'other', 'live', false, false);
  expect(card?.textContent).not.toContain('不能留在旧玩家上');
});

it('removes obsolete tooltip evidence immediately even when Leaflet fades overlays out', () => {
  (map as L.Map & { _fadeAnimated: boolean })._fadeAnimated = true;
  markers.sync([{ ...p, recentProgress: '未来存档变化' }], 'u', 'live', false, false);
  expect(host.querySelector('.world-marker-card')?.textContent).toContain('未来存档变化');
  markers.sync([], 'u', 'history', false, false);
  expect(host.querySelector('.world-marker-card')).toBeNull();
});

it('snaps to the next observation after a connection failure instead of animating across it', () => {
  markers.sync([p], 'u', 'live', false, false);
  markers.sync([p], 'u', 'live', false, true);
  const spy = vi.spyOn(L.Marker.prototype, 'setLatLng');
  const next = { ...p, x: p.x + 1000, observedAt: '2026-09-16T10:00:30Z' };
  markers.sync([next], 'u', 'live', false, false);
  expect(spy).toHaveBeenCalled();
  expect(L.latLng(spy.mock.calls.at(-1)![0]).lat).toBe(projectWorkspaceXY(next.x, next.y)[0]);
});
