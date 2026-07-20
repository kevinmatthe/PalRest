import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { DisplayField, Presentation } from '../contracts/presentation'
import { cloneLayoutProfile, PALWORLD_DEFAULT_LAYOUT, type LayoutProfile } from '../core/layout'
import { HudLayoutEditor } from './HudLayoutEditor'

vi.mock('../components/PalworldMiniMap', () => ({
  PalworldMiniMap: () => <div data-testid="preview-map" />,
}))

afterEach(cleanup)

function availableField(id: string, label: string, value: string | number, progress?: number): DisplayField {
  const kind = typeof value === 'number' ? 'duration_ms' : 'text'
  return { id, label, kind, available: true, value, tone: 'normal', ...(progress === undefined ? {} : { progress }) } as DisplayField
}

function presentation(): Presentation {
  return {
    schema: 'overlay.presentation/v1', game_id: 'palworld', user_id: 'uid-2',
    observed_at: '2026-07-17T04:00:00Z', fresh_until: '2026-07-17T04:00:15Z',
    source_status: 'online', identity: { display_name: 'Lamball', level: 38 },
    map: { x: 1, y: -2, projection: 'palworld_world_v1', tile_set: 'palworld_default_v1', tile_url: '/tiles/{z}/{x}/{y}.png' },
    fields: [
      availableField('identity.account', '账号', 'steam'),
      { id: 'network.latency', label: '延迟', kind: 'latency_ms', available: false, tone: 'muted' },
      availableField('activity.today', '今日已玩', 3_600_000),
      availableField('activity.week', '本周已玩', 7_200_000),
      availableField('policy.strategy', '策略', '限制'),
      availableField('policy.cycle_used', '周期用量', 4_000_000, 0.4),
      availableField('policy.remaining', '剩余', 6_000_000, 0.6),
    ],
  }
}

function renderEditor(
  layout = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT),
  onChange = vi.fn(),
  value = presentation(),
) {
  return {
    onChange,
    ...render(<HudLayoutEditor
      presentation={value}
      layout={layout}
      defaultLayout={PALWORLD_DEFAULT_LAYOUT}
      mapBaseUrl="https://palbox.test"
      onChange={onChange}
    />),
  }
}

describe('HudLayoutEditor', () => {
  it('groups catalog fields and keeps unavailable fields selectable with a clear marker', () => {
    renderEditor()
    const select = screen.getByLabelText('槽位 1 主字段')
    expect(within(select).getByRole('group', { name: '身份' })).toBeInTheDocument()
    expect(within(select).getByRole('group', { name: '在线与网络' })).toBeInTheDocument()
    expect(within(select).getByRole('group', { name: '时长' })).toBeInTheDocument()
    expect(within(select).getByRole('group', { name: '策略' })).toBeInTheDocument()
    const unavailable = within(select).getByRole('option', { name: '延迟（当前不可用）' })
    expect(unavailable).not.toBeDisabled()
  })

  it('renders left primary/fallback and exactly four accessible slot rows', () => {
    renderEditor()
    expect(screen.getByLabelText('左侧主模块')).toHaveValue('map')
    expect(screen.getByLabelText('左侧后备模块')).toHaveValue('player_badge')
    expect(screen.getAllByRole('group', { name: /槽位 \d/ })).toHaveLength(4)
    expect(screen.getByLabelText('槽位 4 后备字段')).toBeInTheDocument()
  })

  it('atomically swaps left modules when either selector chooses its counterpart', () => {
    const primaryChange = renderEditor()
    expect(within(screen.getByLabelText('左侧主模块')).getByRole('option', { name: '玩家徽章' })).not.toBeDisabled()
    fireEvent.change(screen.getByLabelText('左侧主模块'), { target: { value: 'player_badge' } })
    expect(primaryChange.onChange).toHaveBeenCalledWith(expect.objectContaining({
      left: { primary: 'player_badge', fallback: 'map' },
    }))

    cleanup()
    const fallbackChange = renderEditor()
    expect(within(screen.getByLabelText('左侧后备模块')).getByRole('option', { name: '小地图' })).not.toBeDisabled()
    fireEvent.change(screen.getByLabelText('左侧后备模块'), { target: { value: 'map' } })
    expect(fallbackChange.onChange).toHaveBeenCalledWith(expect.objectContaining({
      left: { primary: 'player_badge', fallback: 'map' },
    }))
  })

  it('prevents identical primary and fallback choices and emits an immutable update', () => {
    const { onChange } = renderEditor()
    expect(within(screen.getByLabelText('槽位 1 后备字段')).getByRole('option', { name: '延迟（当前不可用）' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('槽位 1 主字段'), { target: { value: 'identity.account' } })
    const next = onChange.mock.calls[0][0] as LayoutProfile
    expect(next.slots[0]).toEqual({ primary: 'identity.account', fallback: 'presence.last_online' })
    expect(next).not.toBe(PALWORLD_DEFAULT_LAYOUT)
  })

  it('offers all progress modes and limits field mode to catalog entries carrying progress', () => {
    const layout = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    layout.progress = { mode: 'field', field: 'policy.cycle_used' }
    renderEditor(layout)
    expect(within(screen.getByLabelText('进度轨模式')).getAllByRole('option').map((option) => option.textContent))
      .toEqual(['自动', '指定字段', '隐藏'])
    expect(screen.getByLabelText('进度字段')).toBeInTheDocument()
    const progress = screen.getByLabelText('进度字段')
    expect(within(progress).getByRole('option', { name: '周期用量' })).toBeInTheDocument()
    expect(within(progress).getByRole('option', { name: '剩余' })).toBeInTheDocument()
    expect(within(progress).queryByRole('option', { name: '今日已玩' })).not.toBeInTheDocument()
  })

  it('disables field mode when the catalog has no legal progress metadata', () => {
    const value = presentation()
    value.fields = value.fields.map((field) => {
      if (!field.available || field.progress === undefined) return field
      const { progress: _progress, ...withoutProgress } = field
      return withoutProgress
    })
    const { onChange } = renderEditor(cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT), vi.fn(), value)
    const fieldMode = within(screen.getByLabelText('进度轨模式')).getByRole('option', { name: '指定字段' })
    expect(fieldMode).toBeDisabled()
    fireEvent.change(screen.getByLabelText('进度轨模式'), { target: { value: 'field' } })
    expect(onChange).not.toHaveBeenCalled()
  })

  it('keeps a stale saved field visible but disabled without emitting an undefined field', () => {
    const layout = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    layout.progress = { mode: 'field', field: 'policy.removed' }
    const { onChange } = renderEditor(layout)
    const field = screen.getByLabelText('进度字段')
    expect(field).toHaveValue('policy.removed')
    expect(within(field).getByRole('option', { name: 'policy.removed（目录暂未提供）' })).toBeDisabled()
    fireEvent.change(field, { target: { value: '' } })
    expect(onChange).not.toHaveBeenCalled()
  })

  it('selects the first legal catalog entry when switching a stale preference to field mode', () => {
    const layout = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    layout.progress = { mode: 'auto', field: 'policy.removed' }
    const { onChange } = renderEditor(layout)
    fireEvent.change(screen.getByLabelText('进度轨模式'), { target: { value: 'field' } })
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      progress: { mode: 'field', field: 'policy.cycle_used' },
    }))
  })

  it('uses OverlayBar itself for the live preview and passes the validated map base URL', () => {
    renderEditor()
    expect(screen.getByLabelText('HUD 实时预览')).toContainElement(screen.getByLabelText('幻兽帕鲁玩家状态悬浮条'))
    expect(screen.getByTestId('preview-map')).toBeInTheDocument()
    expect(screen.getByRole('listitem', { name: '今日已玩 1小时' })).toBeInTheDocument()
  })

  it('resets only by emitting a fresh copy of the current game default', () => {
    const custom = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    custom.slots[0] = { primary: 'identity.account', fallback: 'network.latency' }
    const { onChange } = renderEditor(custom)
    fireEvent.click(screen.getByRole('button', { name: '恢复当前游戏默认布局' }))
    expect(onChange).toHaveBeenCalledWith(cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT))
    expect(onChange.mock.calls[0][0]).not.toBe(PALWORLD_DEFAULT_LAYOUT)
  })
})
