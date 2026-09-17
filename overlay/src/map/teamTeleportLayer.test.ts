import { expect, it, vi } from 'vitest'
import type * as L from 'leaflet'
import { createTeleportLayer } from './teamTeleportLayer'
const fake = vi.hoisted(() => ({ lines: [] as any[] }))
vi.mock('leaflet', () => ({ polyline: vi.fn((points, options) => {
  const line = { points, options, addTo: vi.fn(), bindTooltip: vi.fn(), setLatLngs: vi.fn(), remove: vi.fn() }
  fake.lines.push(line); return line
}) }))
it('draws a dashed source-to-destination arrow, reuses it, and removes expired arrows', () => {
  fake.lines.length = 0
  const map = { on: vi.fn(), off: vi.fn(), getZoom: () => 2, project: (p: number[]) => ({ x: p[1], y: p[0] }), unproject: (p: number[]) => [p[1], p[0]] } as unknown as L.Map
  const layer = createTeleportLayer(map)
  const event = { id: '1', userID: 'u', playerName: '<b>U</b>', observedAt: 100000,
    from: { id: 'a', name: 'A', coordinate: [-100, 100] as [number, number] }, to: { id: 'b', name: 'B', coordinate: [-200, 200] as [number, number] } }
  layer.update([event])
  expect(fake.lines).toHaveLength(2)
  expect(fake.lines[0].points).toEqual([event.from.coordinate, event.to.coordinate])
  expect(fake.lines[0].options.dashArray).toBe('6 6')
  const label = fake.lines[0].bindTooltip.mock.calls[0][0] as HTMLElement
  expect(label.textContent).toContain('疑似传送')
  expect(label.querySelector('b')).toBeNull()
  layer.update([event])
  expect(fake.lines).toHaveLength(2)
  layer.update([])
  expect(fake.lines.every(line => line.remove.mock.calls.length === 1)).toBe(true)
  layer.destroy()
  expect(map.off).toHaveBeenCalledWith('zoomend', expect.any(Function))
})
