import { readFileSync } from 'node:fs'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { DesktopBridge } from '../core/bridge'
import type { OverlayConfigV1 } from '../core/config'
import { SettingsView } from './SettingsView'

afterEach(cleanup)

function bridge(overrides: Partial<DesktopBridge> = {}): DesktopBridge {
  return {
    fetchSnapshot: vi.fn(), loadConfig: vi.fn(), saveConfig: vi.fn(),
    listPlayers: vi.fn(async () => []), currentWindowLabel: vi.fn(async () => 'settings' as const),
    setAdjustmentMode: vi.fn(), ...overrides,
  }
}

const saved: OverlayConfigV1 = {
  schema: 1, baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'uid-2', scale: 1, locked: true,
}

describe('SettingsView', () => {
  it('keeps essential controls readable and save actions reachable in the settings client area', () => {
    const css = readFileSync('src/styles.css', 'utf8')
    expect(css).toMatch(/\.settings-actions\s*\{[^}]*position:\s*sticky[^}]*bottom:\s*0/s)
    expect(css).toMatch(/\.settings-field\s*>\s*span\s*\{[^}]*font-size:\s*0\.875rem/s)
    expect(css).toMatch(/\.settings-button\s*\{[^}]*min-height:\s*2\.75rem[^}]*font-size:\s*0\.875rem/s)
    expect(css).toMatch(/\.settings-message\s*\{[^}]*font-size:\s*0\.875rem/s)
  })

  it('loads, deduplicates, and selects players only by exact UID', async () => {
    const api = bridge({ listPlayers: vi.fn(async () => [
      { user_id: 'uid-1', name: 'Same', account_name: 'one' },
      { user_id: 'uid-1', name: 'Duplicate', account_name: 'ignored' },
      { user_id: '', name: 'Invalid', account_name: '' },
      { user_id: 'uid-2', name: 'Same', account_name: 'two' },
    ]) })
    render(<SettingsView bridge={api} initialConfig={null} detectedUserId="uid-2" platform="windows" />)
    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: ' https://palbox.test/ ' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))

    await waitFor(() => expect(api.listPlayers).toHaveBeenCalledWith('https://palbox.test', expect.any(AbortSignal)))
    const select = screen.getByLabelText('玩家')
    expect(select.querySelectorAll('option')).toHaveLength(3)
    expect(select).toHaveValue('uid-2')
  })

  it('skips malformed runtime rows and trims stable player identities', async () => {
    const runtimeRows = [
      null, 42, 'row', {},
      { user_id: ' uid-1 ', name: ' First ', account_name: ' account ' },
      { user_id: 'uid-1', name: 'Duplicate', account_name: 'ignored' },
      { user_id: 'uid-2', name: 9, account_name: '' },
      { user_id: 'uid-3', name: 'Third', account_name: 'three' },
    ]
    const api = bridge({ listPlayers: vi.fn(async () => runtimeRows as never) })
    render(<SettingsView bridge={api} initialConfig={null} />)
    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'https://palbox.test' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家').querySelectorAll('option')).toHaveLength(3))
    expect(Array.from(screen.getByLabelText('玩家').querySelectorAll('option')).map((option) => option.value))
      .toEqual(['', 'uid-1', 'uid-3'])
    expect(screen.getByText('First · account — uid-1')).toBeInTheDocument()
  })

  it('never guesses by name and preserves a saved UID only when the list contains it', async () => {
    const api = bridge({ listPlayers: vi.fn(async () => [{ user_id: 'other', name: 'uid-2', account_name: 'uid-2' }]) })
    render(<SettingsView bridge={api} initialConfig={saved} detectedUserId="missing" platform="windows" />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.listPlayers).toHaveBeenCalled())
    expect(screen.getByLabelText('玩家')).toHaveValue('')
  })

  it('validates locally, aborts a prior load, and hides raw failures', async () => {
    const signals: AbortSignal[] = []
    const api = bridge({ listPlayers: vi.fn<DesktopBridge['listPlayers']>((_url, signal) => {
      signals.push(signal)
      if (signals.length === 1 || signals.length === 3) return new Promise<[]>(() => {})
      return Promise.reject(new Error('secret body https://user:pass@host'))
    }) })
    const { unmount } = render(<SettingsView bridge={api} initialConfig={null} />)
    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'bad' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    expect(api.listPlayers).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent('请输入有效的 HTTP 或 HTTPS 服务地址')

    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'https://palbox.test' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    expect(signals[0].aborted).toBe(true)
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('玩家列表加载失败'))
    expect(screen.getByRole('alert')).not.toHaveTextContent(/secret|pass|host/)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    unmount()
    expect(signals[2].aborted).toBe(true)
  })

  it('saves one normalized config, prevents double submit, and enables adjustment', async () => {
    let finish!: () => void
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Lamball', account_name: 'steam' }]),
      saveConfig: vi.fn(() => new Promise<void>((resolve) => { finish = resolve })),
      setAdjustmentMode: vi.fn(async () => {}),
    })
    const onSaved = vi.fn()
    render(<SettingsView bridge={api} initialConfig={saved} onSaved={onSaved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid-2'))
    fireEvent.change(screen.getByLabelText('缩放'), { target: { value: '1.25' } })
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    fireEvent.click(screen.getByRole('button', { name: '正在保存…' }))
    expect(api.saveConfig).toHaveBeenCalledTimes(1)
    expect(api.saveConfig).toHaveBeenCalledWith({ ...saved, scale: 1.25 })
    finish()
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1))
    expect(screen.getByRole('status')).toHaveTextContent('设置已保存')

    fireEvent.click(screen.getByRole('button', { name: '调整悬浮条位置' }))
    expect(api.setAdjustmentMode).toHaveBeenCalledWith(true)
  })

  it('invalidates a loaded player when the service changes and saves only after reloading', async () => {
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Lamball', account_name: 'steam' }]),
      saveConfig: vi.fn(async () => {}),
    })
    render(<SettingsView bridge={api} initialConfig={saved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid-2'))

    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'https://other.test' } })
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    expect(api.saveConfig).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent('重新加载玩家')

    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.listPlayers).toHaveBeenLastCalledWith('https://other.test', expect.any(AbortSignal)))
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    await waitFor(() => expect(api.saveConfig).toHaveBeenCalledWith({ ...saved, baseUrl: 'https://other.test' }))
  })

  it('invalidates an old UID for hanging and failed same-service refreshes until success', async () => {
    let rejectRefresh!: (reason?: unknown) => void
    const refresh = new Promise<never>((_resolve, reject) => { rejectRefresh = reject })
    const listPlayers = vi.fn<DesktopBridge['listPlayers']>()
      .mockResolvedValueOnce([{ user_id: 'uid-2', name: 'Old', account_name: '' }])
      .mockReturnValueOnce(refresh)
      .mockResolvedValueOnce([{ user_id: 'uid-2', name: 'Fresh', account_name: '' }])
    const api = bridge({ listPlayers, saveConfig: vi.fn(async () => {}) })
    render(<SettingsView bridge={api} initialConfig={saved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid-2'))

    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    expect(screen.getByRole('button', { name: '保存设置' })).toBeDisabled()
    expect(screen.getByLabelText('玩家')).toHaveValue('')
    rejectRefresh(new Error('offline'))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('玩家列表加载失败'))
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    expect(api.saveConfig).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid-2'))
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    await waitFor(() => expect(api.saveConfig).toHaveBeenCalledTimes(1))
  })

  it('does not publish save completion after unmount', async () => {
    let finish!: () => void
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      saveConfig: vi.fn(() => new Promise<void>((resolve) => { finish = resolve })),
    })
    const onSaved = vi.fn()
    const { unmount } = render(<SettingsView bridge={api} initialConfig={saved} onSaved={onSaved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid-2'))
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    unmount()
    finish()
    await Promise.resolve()
    expect(onSaved).not.toHaveBeenCalled()
  })

  it('reselect signal clears the saved UID and reloads without selecting it again', async () => {
    const api = bridge({ listPlayers: vi.fn(async () => [
      { user_id: 'uid-2', name: 'Player', account_name: '' },
      { user_id: 'uid-3', name: 'Other', account_name: '' },
    ]) })
    const { rerender } = render(<SettingsView bridge={api} initialConfig={saved} reselectSignal={0} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid-2'))

    rerender(<SettingsView bridge={api} initialConfig={saved} reselectSignal={1} />)
    await waitFor(() => expect(api.listPlayers).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue(''))
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.listPlayers).toHaveBeenCalledTimes(3))
    expect(screen.getByLabelText('玩家')).toHaveValue('')
    fireEvent.change(screen.getByLabelText('玩家'), { target: { value: 'uid-3' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.listPlayers).toHaveBeenCalledTimes(4))
    expect(screen.getByLabelText('玩家')).toHaveValue('uid-3')
  })
})
