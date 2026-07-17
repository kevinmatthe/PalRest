import { readFileSync } from 'node:fs'
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import type { DisplayField, Presentation, SourceStatus } from '../contracts/presentation'
import { cloneLayoutProfile, PALWORLD_DEFAULT_LAYOUT, type LayoutProfile } from '../core/layout'
import { OverlayBar, type OverlayConnectionStatus } from './OverlayBar'

afterEach(cleanup)

function field(id: string, label: string, value: string | number, tone: DisplayField['tone'] = 'normal', progress?: number): DisplayField {
  const kind = typeof value === 'number' ? (id === 'network.latency' ? 'latency_ms' : 'duration_ms') : 'text'
  return { id, label, kind, available: true, value, tone, ...(progress === undefined ? {} : { progress }) } as DisplayField
}

function presentation(sourceStatus: SourceStatus = 'online'): Presentation {
  return {
    schema: 'overlay.presentation/v1', game_id: 'palworld', user_id: 'uid',
    observed_at: '2026-07-16T12:00:00Z', fresh_until: '2026-07-16T12:00:15Z',
    source_status: sourceStatus, identity: { display_name: 'Lamball Keeper', level: 42 },
    map: { x: 128, y: -128, projection: 'palworld_world_v1', tile_set: 'palworld_default_v1', tile_url: '/tiles/{z}/{x}/{y}.png' },
    fields: [
      field('network.latency', '延迟', 39),
      field('presence.last_online', '最后在线', '刚刚'),
      field('activity.today', '今日已玩', 7_200_000),
      field('activity.week', '本周已玩', 21_600_000),
      field('policy.strategy', '策略', '限制模式', 'warning'),
      field('policy.enforcement', '执行', '启用'),
      field('policy.period_end', '周期结束', '周日'),
      field('policy.remaining', '剩余', 3_600_000),
      field('policy.cycle_used', '周期用量', 5_400_000, 'danger', 0.75),
    ],
  }
}

function renderBar(options: Partial<{
  presentation: Presentation; layout: LayoutProfile; status: OverlayConnectionStatus;
  adjustMode: boolean; scale: number; mapBaseUrl: string;
}> = {}) {
  return render(<OverlayBar
    presentation={options.presentation ?? presentation()}
    layout={options.layout ?? cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)}
    status={options.status ?? 'ready'}
    adjustMode={options.adjustMode ?? false}
    scale={options.scale ?? 1}
    mapBaseUrl={options.mapBaseUrl}
  />)
}

describe('OverlayBar', () => {
  it('keeps identity, level, source status, and freshness in the fixed header', () => {
    renderBar()
    const header = screen.getByTestId('identity-header')
    expect(header).toHaveTextContent('Lamball Keeper')
    expect(header).toHaveTextContent('Lv.42')
    expect(header).toHaveTextContent('在线')
    expect(header).toHaveTextContent('更新 2026-07-16 12:00 UTC')
  })

  it('uses the configured left primary and falls back from an unusable or untrusted map to the badge', () => {
    const badgeFirst = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    badgeFirst.left = { primary: 'player_badge', fallback: 'map' }
    const { rerender } = renderBar({ layout: badgeFirst, mapBaseUrl: 'https://palbox.test' })
    expect(screen.getByRole('group', { name: 'Lamball Keeper 玩家徽章' })).toBeInTheDocument()
    expect(screen.queryByTestId('palworld-mini-map-canvas')).not.toBeInTheDocument()

    const mapFirst = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    rerender(<OverlayBar presentation={presentation()} layout={mapFirst} status="ready" adjustMode={false} scale={1} mapBaseUrl="https://palbox.test" />)
    expect(screen.getByTestId('palworld-mini-map-canvas')).toBeInTheDocument()

    const unsafe = presentation()
    unsafe.map = { ...unsafe.map!, tile_url: 'https://attacker.test/tiles/{z}/{x}/{y}.png' }
    rerender(<OverlayBar presentation={unsafe} layout={mapFirst} status="ready" adjustMode={false} scale={1} mapBaseUrl="https://palbox.test" />)
    expect(screen.getByRole('group', { name: 'Lamball Keeper 玩家徽章' })).toBeInTheDocument()
  })

  it('always renders four stable slots in custom order with fallback and stable placeholders', () => {
    const value = presentation()
    value.fields = value.fields.filter(({ id }) => id !== 'activity.today' && id !== 'policy.period_end')
    const layout = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    layout.slots = [
      { primary: 'policy.strategy', fallback: 'policy.enforcement' },
      { primary: 'activity.today', fallback: 'activity.week' },
      { primary: 'unknown.primary', fallback: 'unknown.fallback' },
      { primary: 'network.latency', fallback: 'presence.last_online' },
    ]
    renderBar({ presentation: value, layout })

    const slots = within(screen.getByRole('list', { name: '玩家状态字段' })).getAllByRole('listitem')
    expect(slots).toHaveLength(4)
    expect(slots.map((slot) => within(slot).getByRole('term').textContent)).toEqual(['策略', '本周已玩', 'unknown.primary', '延迟'])
    expect(slots.map((slot) => within(slot).getByRole('definition').textContent)).toEqual(['限制模式', '6小时', '--', '39 ms'])
  })

  it('applies field tones locally and the highest available provider risk globally', () => {
    renderBar()
    expect(screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')).toHaveClass('overlay--danger')
    expect(screen.getByRole('listitem', { name: '策略 限制模式' })).toHaveClass('overlay__field--warning')
    expect(screen.getByRole('progressbar')).toHaveClass('overlay__progress--danger')
  })

  it.each([
    [{ mode: 'auto', field: 'policy.cycle_used' } as const, '75'],
    [{ mode: 'field', field: 'policy.cycle_used' } as const, '75'],
    [{ mode: 'hidden' } as const, null],
  ])('resolves progress mode %#', (progress, expected) => {
    const layout = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    layout.progress = progress
    renderBar({ layout })
    if (expected === null) expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
    else expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', expected)
  })

  it.each([
    ['offline', 'ready'], ['online', 'stale'], ['online', 'disconnected'],
  ] as const)('desaturates %s/%s while preserving last-good content', (sourceStatus, status) => {
    renderBar({ presentation: presentation(sourceStatus), status })
    const overlay = screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')
    expect(overlay).toHaveClass(sourceStatus === 'offline' ? 'overlay--offline' : `overlay--${status}`)
    expect(screen.getByText('限制模式')).toBeInTheDocument()
  })

  it('supports deep Tauri dragging only in adjustment mode', () => {
    const { rerender } = renderBar()
    const overlay = screen.getByLabelText('幻兽帕鲁玩家状态悬浮条')
    expect(overlay).not.toHaveAttribute('data-tauri-drag-region')
    rerender(<OverlayBar presentation={presentation()} layout={cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)} status="ready" adjustMode scale={0.85} mapBaseUrl="https://palbox.test" />)
    expect(overlay).toHaveAttribute('data-tauri-drag-region', 'deep')
    expect(overlay).toHaveStyle({ '--overlay-scale': '0.85' })
    expect(screen.getByText('拖动调整位置')).not.toHaveAttribute('data-tauri-drag-region')
  })

  it('locks the approved HUD geometry without leaking it into settings selectors', () => {
    renderBar()
    const css = readFileSync('src/styles.css', 'utf8')
    expect(css).toMatch(/\.overlay\s*\{[^}]*--overlay-width:\s*30rem[^}]*--overlay-height:\s*4\.75rem[^}]*--overlay-map-size:\s*3\.875rem/s)
    expect(css).toMatch(/\.overlay\s*\{[^}]*--overlay-panel:\s*rgba\(8,\s*18,\s*22,\s*0\.84\)/s)
    expect(css).toMatch(/\.overlay\s*\{[^}]*width:\s*min\(100%,\s*var\(--overlay-width\)\)[^}]*height:\s*min\(100%,\s*var\(--overlay-height\)\)/s)
    expect(css).toMatch(/\.overlay__frame\s*\{[^}]*padding:\s*0\.375rem/s)
    expect(css).toMatch(/\.overlay__frame\s*\{[^}]*grid-template-columns:\s*var\(--overlay-map-size\)\s+minmax\(0,\s*1fr\)/s)
    expect(css).toMatch(/\.player-badge\s*\{[^}]*width:\s*var\(--overlay-map-size\)[^}]*height:\s*var\(--overlay-map-size\)/s)
    expect(css).toMatch(/\.overlay__content\s*\{[^}]*grid-template-rows:\s*1\.5625rem\s+1\.9375rem/s)
    expect(css).toMatch(/\.overlay__fields\s*\{[^}]*grid-template-columns:\s*repeat\(4,\s*minmax\(0,\s*1fr\)\)/s)
    expect(css).toMatch(/\.overlay__progress\s*\{[^}]*height:\s*2px/s)
    expect(css).not.toMatch(/\.overlay\s*\{[^}]*repeating-linear-gradient/s)
    expect(css).not.toMatch(/\.settings[^,{]*,\s*\.overlay/)
  })
})
