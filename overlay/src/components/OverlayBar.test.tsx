import { readFileSync } from 'node:fs'
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import type { Snapshot, SourceStatus, TimerTone } from '../contracts/snapshot'
import { OverlayBar } from './OverlayBar'

afterEach(cleanup)

function canonicalSnapshot(
  sourceStatus: SourceStatus = 'online',
  tones: TimerTone[] = ['normal', 'normal', 'warning', 'warning'],
): Snapshot {
  const labels = ['今日已玩', '本周已玩', '本周期已用', '频控剩余']
  const values = [7_200_000, 21_600_000, 5_400_000, 3_600_000]
  return {
    schema: 'overlay.snapshot/v1',
    game_id: 'palworld',
    user_id: 'steam_player',
    observed_at: '2026-07-16T12:00:00Z',
    fresh_until: '2026-07-16T12:00:15Z',
    source_status: sourceStatus,
    capabilities: ['identity', 'latency', 'timers', 'map'],
    identity: { display_name: 'Lamball Keeper', level: 42 },
    latency: { milliseconds: 38.5 },
    timers: labels.map((label, index) => ({
      id: `timer-${index}`,
      label,
      value_ms: values[index],
      semantic: 'duration',
      tone: tones[index] ?? 'normal',
      progress: index === 3 ? 0.25 : undefined,
    })),
    map: {
      x: 187.25,
      y: -64.5,
      projection: 'palworld_world_v1',
      tile_set: 'palworld_default_v1',
      tile_url: '/map/tiles/{z}/{x}/{y}.png',
    },
  }
}

describe('OverlayBar', () => {
  it('uses compact nominal overlay semantics and preserves provider timer order', () => {
    render(<OverlayBar snapshot={canonicalSnapshot()} />)

    const overlay = screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')
    expect(overlay).toHaveClass('overlay', 'overlay--compact', 'overlay--warning')
    expect(overlay).toHaveStyle({ '--overlay-scale': '1' })

    const timerRegion = screen.getByTestId('capability-timers')
    expect(within(timerRegion).getAllByRole('term').map((node) => node.textContent)).toEqual([
      '今日已玩',
      '本周已玩',
      '本周期已用',
      '频控剩余',
    ])
    expect(within(timerRegion).getAllByRole('definition').map((node) => node.textContent)).toEqual([
      '2小时',
      '6小时',
      '1小时30分钟',
      '1小时',
    ])

    const css = readFileSync('src/styles.css', 'utf8')
    expect(css).toMatch(/html,\s*body,\s*#root\s*\{[^}]*margin:\s*0[^}]*padding:\s*0[^}]*width:\s*100%[^}]*height:\s*100%[^}]*overflow:\s*hidden[^}]*background:\s*transparent/s)
    expect(css).toMatch(/--overlay-width:\s*30rem/)
    expect(css).toMatch(/--overlay-height:\s*4\.75rem/)
    expect(css).toMatch(/--overlay-map-size:\s*3\.875rem/)
    expect(css).toMatch(/width:\s*min\(100%,\s*var\(--overlay-width\)\)/)
    expect(css).toMatch(/height:\s*min\(100%,\s*var\(--overlay-height\)\)/)
    expect(css).toMatch(/width:\s*var\(--overlay-map-size\)/)
    expect(css).toMatch(/height:\s*var\(--overlay-map-size\)/)
    expect(css).not.toMatch(/var\(--overlay-(?:width|height|map-size)\)\s*\*\s*var\(--overlay-scale\)/)
    expect(css).toMatch(/\.overlay\s*\{[^}]*--overlay-panel:\s*rgba\(8,\s*18,\s*22,\s*0\.84\)/s)
    expect(css).toMatch(/\.overlay\s*\{[^}]*border-radius:\s*0\.875rem/s)
    expect(css).toMatch(/\.overlay\s*\{[^}]*backdrop-filter:\s*blur\(1rem\)\s+saturate\(115%\)/s)
    expect(css).toMatch(/\.overlay__frame\s*\{[^}]*padding:\s*0\.375rem/s)
    expect(css).not.toMatch(/\.overlay__frame\s*\{[^}]*padding-bottom:/s)
    expect(css).toMatch(/\.overlay__content\s*\{[^}]*grid-template-rows:\s*1\.5625rem\s+1\.9375rem/s)
    expect(css).toMatch(/\.overlay__telemetry\s*\{[^}]*border-bottom:\s*1px solid rgba\(255,\s*255,\s*255,\s*0\.06\)/s)
    expect(css).toMatch(/\.overlay__timers\s*\{[^}]*grid-template-columns:\s*repeat\(4,\s*minmax\(0,\s*1fr\)\)/s)
    expect(css).not.toMatch(/\.overlay\s*\{[^}]*repeating-linear-gradient/s)
    expect(css).toMatch(/\.overlay-state\s*\{[^}]*border-radius:\s*0\.875rem[^}]*background:\s*rgba\(8,\s*18,\s*22,\s*0\.88\)/s)
  })

  it('applies scale once per readable text role instead of compounding em sizes', () => {
    render(<OverlayBar snapshot={canonicalSnapshot()} scale={0.8} status="stale" />)
    expect(screen.getAllByRole('term')).toHaveLength(4)
    expect(screen.getByLabelText('数据状态')).toHaveTextContent('数据已过期 · 最后更新')

    const css = readFileSync('src/styles.css', 'utf8')
    expect(css).toMatch(/\.overlay\s*\{[^}]*font-size:\s*1rem/s)
    expect(css).not.toMatch(/\.overlay\s*\{[^}]*font-size:[^;]*var\(--overlay-scale\)/s)
    expect(css).toMatch(/\.overlay__name\s*\{[^}]*font-size:\s*clamp\(0\.6875rem,[^;]*var\(--overlay-scale\)[^;]*0\.8125rem\)/s)
    expect(css).toMatch(/\.overlay__source-status\s*\{[^}]*font-size:\s*clamp\(0\.5625rem,[^;]*var\(--overlay-scale\)[^;]*0\.6875rem\)/s)
    expect(css).toMatch(/\.overlay__meta\s*\{[^}]*font-size:\s*clamp\(0\.5rem,[^;]*var\(--overlay-scale\)[^;]*0\.625rem\)/s)
    expect(css).toMatch(/\.overlay__connection\s*\{[^}]*flex:\s*0\s+0\s+auto/s)
    expect(css).toMatch(/\.overlay__latency\s*\{[^}]*flex:\s*0\s+1\s+auto[^}]*min-width:\s*0/s)
    expect(css).toMatch(/\.overlay__locator-label\s*\{[^}]*font-size:\s*clamp\(0\.5rem,/s)
    expect(css).toMatch(/\.overlay__coordinates\s*\{[^}]*font-size:\s*clamp\(0\.5rem,/s)
    expect(css).toMatch(/\.overlay__timer dt\s*\{[^}]*font-size:\s*clamp\(0\.5rem,/s)
    expect(css).toMatch(/\.overlay__timer dd\s*\{[^}]*font-size:\s*clamp\(0\.625rem,/s)
  })

  it.each([
    ['online', '在线'],
    ['offline', '离线'],
    ['unknown', '状态未知'],
  ] as const)('shows identity, %s status, ping, and freshness', (status, copy) => {
    render(<OverlayBar snapshot={canonicalSnapshot(status)} />)

    const overlay = screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')
    if (status === 'offline') expect(overlay).toHaveClass('overlay--offline')
    else expect(overlay).not.toHaveClass('overlay--offline')
    expect(screen.getByText('Lamball Keeper · Lv.42')).toBeInTheDocument()
    expect(screen.getByText(copy)).toBeInTheDocument()
    expect(screen.getByText('39 ms')).toBeInTheDocument()
    expect(screen.getByText('更新 2026-07-16 12:00 UTC')).toBeInTheDocument()
    expect(screen.queryByText(/当前数据/)).not.toBeInTheDocument()
    expect(screen.getAllByText(/2026-07-16 12:00 UTC/)).toHaveLength(1)
  })

  it('uses warning and danger styling without disruptive alert behavior', () => {
    const css = readFileSync('src/styles.css', 'utf8')
    expect(css).toMatch(/\.overlay--warning\s*\{[^}]*--overlay-accent:\s*var\(--overlay-amber\)[^}]*--overlay-edge:/s)
    expect(css).toMatch(/\.overlay--danger\s*\{[^}]*--overlay-accent:\s*var\(--overlay-red\)[^}]*--overlay-edge:/s)
    const { rerender } = render(<OverlayBar snapshot={canonicalSnapshot()} />)
    const overlay = screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')
    expect(overlay).toHaveClass('overlay--warning')
    expect(overlay).not.toHaveClass('blink', 'animate', 'overlay--alert')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    rerender(<OverlayBar snapshot={canonicalSnapshot('online', ['normal', 'danger', 'warning', 'normal'])} />)
    expect(overlay).toHaveClass('overlay--danger')
    expect(overlay).not.toHaveClass('overlay--warning')
    expect(screen.getByRole('progressbar')).toHaveClass('overlay__progress--danger')
  })

  it.each([
    ['ready', '更新 2026-07-16 12:00 UTC'],
    ['stale', '数据已过期 · 最后更新 2026-07-16 12:00 UTC'],
    ['disconnected', '连接已断开 · 最后更新 2026-07-16 12:00 UTC'],
  ] as const)('retains snapshot values while the poller state is %s', (status, copy) => {
    render(<OverlayBar snapshot={canonicalSnapshot()} status={status} />)

    expect(screen.getByText('Lamball Keeper · Lv.42')).toBeInTheDocument()
    expect(screen.getByText('频控剩余')).toBeInTheDocument()
    expect(screen.getByLabelText('数据状态')).toHaveTextContent(copy)
    expect(screen.getAllByText(/2026-07-16 12:00 UTC/)).toHaveLength(1)
  })

  it('uses policy cycle usage rather than inverse remaining progress for the rail', () => {
    const value = canonicalSnapshot()
    value.timers![2] = {
      ...value.timers![2],
      id: 'policy_cycle_used',
      progress: 0.75,
    }
    value.timers![3] = {
      ...value.timers![3],
      id: 'policy_remaining',
      progress: 0.25,
    }

    render(<OverlayBar snapshot={value} />)
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '75')
    expect(screen.getByRole('progressbar')).toHaveAccessibleName('本周期已用进度')
  })

  it('removes only capability regions whose data is absent', () => {
    const value = canonicalSnapshot()
    value.capabilities = ['identity']
    delete value.latency
    delete value.timers
    delete value.map
    render(<OverlayBar snapshot={value} />)

    expect(screen.getByTestId('capability-identity')).toHaveTextContent('Lamball Keeper · Lv.42')
    expect(screen.queryByTestId('capability-latency')).not.toBeInTheDocument()
    expect(screen.queryByTestId('capability-timers')).not.toBeInTheDocument()
    expect(screen.queryByTestId('capability-map')).not.toBeInTheDocument()
  })

  it('renders the private Palworld map only when map capability and base URL are present', () => {
    const { rerender } = render(
      <OverlayBar snapshot={canonicalSnapshot()} mapBaseUrl="https://palbox.tailnet.ts.net:9443/" />,
    )
    const position = screen.getByTestId('capability-map')

    expect(within(position).getByTestId('palworld-mini-map-canvas')).toBeInTheDocument()

    const value = canonicalSnapshot()
    value.capabilities = value.capabilities.filter((capability) => capability !== 'map')
    delete value.map
    rerender(<OverlayBar snapshot={value} />)
    expect(screen.queryByTestId('capability-map')).not.toBeInTheDocument()
  })

  it('renders an unavailable map region without a private base and preserves other stats', () => {
    render(<OverlayBar snapshot={canonicalSnapshot()} />)

    expect(screen.getByTestId('capability-map')).toBeInTheDocument()
    expect(within(screen.getByTestId('capability-map')).getByRole('status')).toHaveTextContent('地图不可用')
    expect(screen.getByText('Lamball Keeper · Lv.42')).toBeInTheDocument()
    expect(screen.getByText('频控剩余')).toBeInTheDocument()
  })

  it('adds a 44px drag affordance only in adjust mode', () => {
    const { rerender } = render(<OverlayBar snapshot={canonicalSnapshot()} />)
    expect(screen.queryByText('拖动调整位置')).not.toBeInTheDocument()
    expect(screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')).not.toHaveAttribute('data-tauri-drag-region')

    rerender(<OverlayBar snapshot={canonicalSnapshot()} adjustMode scale={0.85} />)
    const overlay = screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')
    const dragHint = screen.getByText('拖动调整位置')
    expect(overlay).toHaveAttribute('data-tauri-drag-region', 'deep')
    expect(overlay).toHaveStyle({ '--overlay-scale': '0.85' })
    expect(dragHint).toHaveClass('overlay__drag-hint')
    expect(dragHint).not.toHaveAttribute('data-tauri-drag-region')
    expect(screen.getByTestId('capability-identity')).toBeInstanceOf(HTMLElement)
    expect(screen.getByTestId('capability-timers')).toBeInstanceOf(HTMLElement)

    rerender(<OverlayBar snapshot={canonicalSnapshot()} adjustMode scale={0.1} />)
    expect(overlay).toHaveStyle({ '--overlay-scale': '0.8' })
    rerender(<OverlayBar snapshot={canonicalSnapshot()} adjustMode scale={3} />)
    expect(overlay).toHaveStyle({ '--overlay-scale': '1.25' })

    const css = readFileSync('src/styles.css', 'utf8')
    expect(css).toMatch(/\.overlay__drag-hint\s*\{[^}]*min-height:\s*2\.75rem/s)
    expect(css).toMatch(/\.overlay__drag-hint\s*>\s*\*\s*\{[^}]*pointer-events:\s*none/s)
    expect(css).toMatch(/\.overlay--adjusting\s*\{[^}]*inset/s)
    expect(css).not.toMatch(/outline-offset:\s*[1-9]/)
  })
})
