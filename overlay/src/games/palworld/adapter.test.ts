import type { Snapshot, Timer, TimerTone } from '../../contracts/snapshot'
import { describe, expect, it } from 'vitest'
import { palworldAdapter } from './adapter'

function timer(tone: TimerTone): Timer {
  return {
    id: tone,
    label: `服务端标签-${tone}`,
    value_ms: 0,
    semantic: 'duration',
    tone,
  }
}

function snapshot(tones: TimerTone[]): Snapshot {
  return {
    schema: 'overlay.snapshot/v1',
    game_id: 'palworld',
    user_id: 'steam_player',
    observed_at: '2026-07-16T12:00:00Z',
    fresh_until: '2026-07-16T12:00:15Z',
    source_status: 'online',
    capabilities: ['identity', 'timers'],
    identity: { display_name: '测试玩家', level: 42 },
    timers: tones.map(timer),
  }
}

describe('palworldAdapter', () => {
  it('describes Palworld and its platform process hints', () => {
    expect(palworldAdapter).toMatchObject({
      id: 'palworld',
      title: '幻兽帕鲁',
      processHints: {
        windows: ['Palworld-Win64-Shipping.exe'],
        macos: ['Palworld.app'],
      },
    })
  })

  it.each([
    [0, '0分钟'],
    [29_999, '0分钟'],
    [30_000, '1分钟'],
    [-30_000, '-1分钟'],
    [89_999, '1分钟'],
    [90_000, '2分钟'],
    [60 * 60_000, '1小时'],
    [90 * 60_000, '1小时30分钟'],
    [24 * 60 * 60_000, '1天'],
    [25 * 60 * 60_000 + 5 * 60_000, '1天1小时5分钟'],
  ])('formats %i milliseconds as %s', (milliseconds, expected) => {
    expect(palworldAdapter.formatDuration(milliseconds)).toBe(expected)
  })

  it('selects the strongest timer tone independent of provider order', () => {
    expect(palworldAdapter.overallTone(snapshot([]))).toBe('muted')
    expect(palworldAdapter.overallTone(snapshot(['muted', 'normal']))).toBe('normal')
    expect(palworldAdapter.overallTone(snapshot(['danger', 'warning']))).toBe('danger')
    expect(palworldAdapter.overallTone(snapshot(['warning', 'danger']))).toBe('danger')
    expect(palworldAdapter.overallTone(snapshot(['normal', 'warning', 'warning']))).toBe('warning')
  })
})
