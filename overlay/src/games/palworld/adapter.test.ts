import { describe, expect, it } from 'vitest'
import { PALWORLD_DEFAULT_LAYOUT } from '../../core/layout'
import type { GameAdapter } from '../types'
import { palworldAdapter } from './adapter'

type Equal<Left, Right> =
  (<Value>() => Value extends Left ? 1 : 2) extends
  (<Value>() => Value extends Right ? 1 : 2)
    ? true
    : false
type Expect<Value extends true> = Value
type DefaultLayoutIsReadonly = Equal<
  Pick<GameAdapter, 'defaultLayout'>,
  { -readonly [Key in 'defaultLayout']: GameAdapter[Key] }
> extends true ? false : true

const defaultLayoutIsReadonly: Expect<DefaultLayoutIsReadonly> = true

describe('palworldAdapter', () => {
  it('exposes the adapter default property as readonly', () => {
    expect(defaultLayoutIsReadonly).toBe(true)
  })

  it('describes Palworld and its platform process hints', () => {
    expect(palworldAdapter).toMatchObject({
      id: 'palworld',
      title: '幻兽帕鲁',
      processHints: {
        windows: ['Palworld-Win64-Shipping.exe'],
        macos: ['Palworld.app'],
      },
      defaultLayout: PALWORLD_DEFAULT_LAYOUT,
    })
  })

  it('returns an isolated editable copy of the default layout on every read', () => {
    const first = palworldAdapter.defaultLayout
    const second = palworldAdapter.defaultLayout

    expect(first).toEqual(PALWORLD_DEFAULT_LAYOUT)
    expect(first).not.toBe(second)
    expect(first.left).not.toBe(second.left)
    expect(first.slots).not.toBe(second.slots)
    expect(first.slots[0]).not.toBe(second.slots[0])
    expect(first.progress).not.toBe(second.progress)

    first.left.primary = 'player_badge'
    first.slots[0].primary = 'changed.field'
    first.progress.field = 'changed.progress'

    expect(palworldAdapter.defaultLayout).toEqual(PALWORLD_DEFAULT_LAYOUT)
  })

})
