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
    expect(css).toMatch(/--overlay-width:\s*30rem/)
    expect(css).toMatch(/--overlay-height:\s*4\.75rem/)
    expect(css).toMatch(/--overlay-map-size:\s*3\.875rem/)
  })

  it.each([
    ['online', '在线'],
    ['offline', '离线'],
    ['unknown', '状态未知'],
  ] as const)('shows identity, %s status, ping, and freshness', (status, copy) => {
    render(<OverlayBar snapshot={canonicalSnapshot(status)} />)

    expect(screen.getByText('Lamball Keeper · Lv.42')).toBeInTheDocument()
    expect(screen.getByText(copy)).toBeInTheDocument()
    expect(screen.getByText('39 ms')).toBeInTheDocument()
    expect(screen.getByText('更新 2026-07-16 12:00 UTC')).toBeInTheDocument()
  })

  it('uses warning and danger styling without disruptive alert behavior', () => {
    const { rerender } = render(<OverlayBar snapshot={canonicalSnapshot()} />)
    const overlay = screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')
    expect(overlay).toHaveClass('overlay--warning')
    expect(overlay).not.toHaveClass('blink', 'animate', 'overlay--alert')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    rerender(<OverlayBar snapshot={canonicalSnapshot('online', ['normal', 'danger', 'warning', 'normal'])} />)
    expect(overlay).toHaveClass('overlay--danger')
    expect(overlay).not.toHaveClass('overlay--warning')
  })

  it.each([
    ['ready', '在线 · 当前数据'],
    ['stale', '数据已过期 · 最后更新 2026-07-16 12:00 UTC'],
    ['disconnected', '连接已断开 · 最后更新 2026-07-16 12:00 UTC'],
  ] as const)('retains snapshot values while the poller state is %s', (status, copy) => {
    render(<OverlayBar snapshot={canonicalSnapshot()} status={status} />)

    expect(screen.getByText('Lamball Keeper · Lv.42')).toBeInTheDocument()
    expect(screen.getByText('频控剩余')).toBeInTheDocument()
    expect(screen.getByLabelText('数据状态')).toHaveTextContent(copy)
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

  it('adds a 44px drag affordance only in adjust mode', () => {
    const { rerender } = render(<OverlayBar snapshot={canonicalSnapshot()} />)
    expect(screen.queryByText('拖动调整位置')).not.toBeInTheDocument()
    expect(screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')).not.toHaveAttribute('data-tauri-drag-region')

    rerender(<OverlayBar snapshot={canonicalSnapshot()} adjustMode scale={0.85} />)
    const overlay = screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')
    const dragHint = screen.getByText('拖动调整位置')
    expect(overlay).toHaveAttribute('data-tauri-drag-region')
    expect(overlay).toHaveStyle({ '--overlay-scale': '0.85' })
    expect(dragHint).toHaveClass('overlay__drag-hint')

    const css = readFileSync('src/styles.css', 'utf8')
    expect(css).toMatch(/\.overlay__drag-hint\s*\{[^}]*min-height:\s*2\.75rem/s)
  })
})
