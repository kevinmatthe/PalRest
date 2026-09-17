import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { TeamMap } from './TeamMap'
import type { DesktopBridge } from '../core/bridge'
const mocks = vi.hoisted(() => {
  const map = { setView: vi.fn(), fitBounds: vi.fn(), remove: vi.fn(), on: vi.fn(), off: vi.fn(), invalidateSize: vi.fn(), project: vi.fn((p: number[]) => ({ x: p[1], y: p[0] })), getZoom: () => 2, unproject: (p: number[]) => [p[1], p[0]] }
  const markers: any[] = []
  return { map, markers, lines: [] as any[], createMap: vi.fn(() => map) }
})
vi.mock('leaflet', () => ({
  CRS: { Simple: {} }, map: mocks.createMap,
  tileLayer: () => ({ addTo: vi.fn(), on: vi.fn(), off: vi.fn() }),
  polyline: (points: unknown, options: unknown) => { const line = { points, options, addTo: vi.fn(), bindTooltip: vi.fn(), setLatLngs: vi.fn(), remove: vi.fn() }; mocks.lines.push(line); return line },
  circleMarker: () => {
    const marker: any = { addTo: vi.fn(), setLatLng: vi.fn(), setStyle: vi.fn(), setRadius: vi.fn(), bindTooltip: vi.fn(), setTooltipContent: vi.fn(), bindPopup: vi.fn(), setPopupContent: vi.fn(), remove: vi.fn() }
    mocks.markers.push(marker); return marker
  },
}))
afterEach(() => { cleanup(); vi.clearAllMocks(); mocks.markers.length = 0; mocks.lines.length = 0 })
const data = { as_of: new Date().toISOString(), online_count: 2, positioned: 2, players: [{ user_id: 'me', name: '我', x: 100, y: -100 }, { user_id: 'other', name: '<img src=x>', x: 200, y: -200 }] }
const config = { schema: 1 as const, baseUrl: 'https://box.test', gameId: 'palworld' as const, userId: 'me', scale: 1 as const, locked: true }
it('shows everyone, follows self on demand, uses text nodes for names and destroys its map', async () => {
  const bridge = { fetchLivePositions: vi.fn(async () => data), closeTeamMap: vi.fn(async () => {}) } as unknown as DesktopBridge
  const { unmount } = render(<TeamMap bridge={bridge} config={config} />)
  await screen.findByText('2 人在线 · 2 人有位置')
  expect(mocks.map.fitBounds).toHaveBeenCalled()
  expect(mocks.markers).toHaveLength(2)
  const tooltip = mocks.markers[1].bindTooltip.mock.calls[0][0] as HTMLElement
  expect(tooltip.textContent).toContain('<img src=x>')
  expect(tooltip.querySelector('img')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: '跟随自己' }))
  expect(mocks.map.setView).toHaveBeenLastCalledWith([-100, 100], 2, { animate: false })
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(bridge.closeTeamMap).toHaveBeenCalledTimes(1)
  unmount()
  expect(mocks.map.remove).toHaveBeenCalledTimes(1)
})
it('keeps offline self optional and reports an empty server without invented positions', async () => {
  const bridge = { fetchLivePositions: vi.fn(async () => ({ ...data, online_count: 0, positioned: 0, players: [] })) } as unknown as DesktopBridge
  render(<TeamMap bridge={bridge} config={config} />)
  await waitFor(() => expect(screen.getByText('当前没有可显示的玩家位置')).toBeInTheDocument())
  expect(screen.getByRole('button', { name: '跟随自己' })).toBeDisabled()
  expect(mocks.markers).toHaveLength(0)
})
it('preserves manual zoom across refreshes and reuses markers for unchanged positions', async () => {
  vi.useFakeTimers()
  try {
    const bridge = { fetchLivePositions: vi.fn(async () => ({ ...data, as_of: new Date().toISOString() })) } as unknown as DesktopBridge
    render(<TeamMap bridge={bridge} config={config} />)
    await act(async () => {})
    const zoomStart = mocks.map.on.mock.calls.find(([name]) => name === 'zoomstart')![1] as () => void
    act(() => zoomStart())
    const fits = mocks.map.fitBounds.mock.calls.length
    const count = mocks.markers.length
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(mocks.map.fitBounds).toHaveBeenCalledTimes(fits)
    expect(mocks.markers).toHaveLength(count)
    expect(screen.getByText('自由浏览')).toBeInTheDocument()
  } finally { cleanup(); vi.useRealTimers() }
})

it('shows a temporary dashed teleport between two fast-travel points', async () => {
  vi.useFakeTimers(); vi.setSystemTime(100000)
  try {
    const fetch = vi.fn(async () => ({ ...data, as_of: new Date().toISOString(), players: [{ user_id: 'me', name: '我', x: -108666.8, y: 79119.9 }] }))
    render(<TeamMap bridge={{ fetchLivePositions: fetch } as unknown as DesktopBridge} config={config} />)
    await act(async () => {})
    fetch.mockImplementation(async () => ({ ...data, as_of: new Date().toISOString(), players: [{ user_id: 'me', name: '我', x: -265220, y: 173530 }] }))
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(screen.getByText(/疑似传送 1 次/)).toBeInTheDocument()
    expect(mocks.lines).toHaveLength(2)
    expect(mocks.lines[0].options).toMatchObject({ dashArray: '6 6' })
    await act(async () => { await vi.advanceTimersByTimeAsync(30000) })
    expect(screen.queryByText(/疑似传送 1 次/)).not.toBeInTheDocument()
    expect(mocks.lines.every(line => line.remove.mock.calls.length === 1)).toBe(true)
  } finally { cleanup(); vi.useRealTimers() }
})
