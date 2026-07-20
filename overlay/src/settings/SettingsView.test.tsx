import { readFileSync } from 'node:fs'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { DesktopBridge } from '../core/bridge'
import type { FetchPresentationResult } from '../core/bridge'
import type { Presentation } from '../contracts/presentation'
import type { OverlayConfigV1 } from '../core/config'
import { cloneLayoutProfile, PALWORLD_DEFAULT_LAYOUT } from '../core/layout'
import { SettingsView } from './SettingsView'

afterEach(cleanup)

function bridge(overrides: Partial<DesktopBridge> = {}): DesktopBridge {
  return {
    fetchPresentation: vi.fn(async () => ({ status: 200 as const, body: preview })), loadConfig: vi.fn(), saveConfig: vi.fn(),
    listPlayers: vi.fn(async () => []), currentWindowLabel: vi.fn(async () => 'settings' as const),
    setAdjustmentMode: vi.fn(), ...overrides,
  }
}

function cssDeclarations(css: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const match = new RegExp(`(?:^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, 's').exec(css)
  if (!match) throw new Error(`missing CSS selector: ${selector}`)
  return match[1]
}

const saved: OverlayConfigV1 = {
  schema: 2, baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'uid-2', scale: 1, locked: true,
  layouts: { palworld: cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT) },
}

const preview: Presentation = {
  schema: 'overlay.presentation/v1', game_id: 'palworld', user_id: 'uid-2',
  observed_at: '2026-07-17T04:00:00Z', fresh_until: '2026-07-17T04:00:15Z',
  source_status: 'online', identity: { display_name: 'Preview Player' }, fields: [
    { id: 'network.latency', label: '延迟', kind: 'latency_ms', available: true, value: 35, tone: 'normal' },
    { id: 'presence.last_online', label: '最后在线', kind: 'text', available: true, value: '现在', tone: 'normal' },
    { id: 'activity.today', label: '今日已玩', kind: 'duration_ms', available: true, value: 3_600_000, tone: 'normal' },
    { id: 'activity.week', label: '本周已玩', kind: 'duration_ms', available: true, value: 7_200_000, tone: 'normal' },
    { id: 'policy.strategy', label: '策略', kind: 'text', available: true, value: '限制', tone: 'normal' },
    { id: 'policy.enforcement', label: '执行', kind: 'status', available: true, value: '启用', tone: 'normal' },
    { id: 'policy.period_end', label: '周期结束', kind: 'text', available: true, value: '周日', tone: 'normal' },
    { id: 'policy.remaining', label: '剩余', kind: 'duration_ms', available: true, value: 5_000_000, tone: 'normal' },
  ],
}

describe('SettingsView', () => {
  it('keeps essential controls readable and save actions reachable in the settings client area', () => {
    const css = readFileSync('src/styles.css', 'utf8')
    expect(css).toMatch(/html\.settings-window,\s*body\.settings-window\s*\{[^}]*overflow:\s*hidden/s)
    expect(css).toMatch(/\.settings-shell\s*\{[^}]*height:\s*100%[^}]*grid-template-rows:\s*auto minmax\(0,\s*1fr\)[^}]*overflow:\s*hidden/s)
    expect(css).toMatch(/\.settings-form\s*\{[^}]*grid-template-rows:\s*minmax\(0,\s*1fr\) auto/s)
    expect(css).toMatch(/\.settings-form__content\s*\{[^}]*overflow-y:\s*auto/s)
    expect(css).toMatch(/\.settings-actions\s*\{[^}]*position:\s*relative/s)
    expect(css).toMatch(/\.settings-actions\s*\{[^}]*flex:\s*none/s)
    expect(css).toMatch(/\.hud-editor\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/s)
    expect(css).toMatch(/\.hud-editor__preview\s*\{[^}]*order:\s*-1/s)
    expect(css).toMatch(/\.hud-editor select\s*\{[^}]*min-height:\s*2\.75rem/s)
    const previewStage = cssDeclarations(css, '.hud-editor__preview-stage')
    expect(previewStage).toMatch(/overflow-x:\s*auto/)
    const previewOverlay = cssDeclarations(css, '.hud-editor__preview-stage .overlay')
    expect(previewOverlay).toMatch(/width:\s*30rem/)
    expect(previewOverlay).toMatch(/min-width:\s*30rem/)
    expect(previewOverlay).toMatch(/max-width:\s*none/)
    expect(previewOverlay).toMatch(/height:\s*4\.75rem/)
    expect(previewOverlay).toMatch(/min-height:\s*4\.75rem/)
    const globalOverlay = cssDeclarations(css, '.overlay')
    expect(globalOverlay).toMatch(/width:\s*min\(100%,\s*var\(--overlay-width\)\)/)
    expect(globalOverlay).toMatch(/height:\s*min\(100%,\s*var\(--overlay-height\)\)/)
    expect(globalOverlay).not.toMatch(/max-width:\s*none/)
    expect(css).toMatch(/\.settings-field\s*>\s*span\s*\{[^}]*font-size:\s*0\.875rem/s)
    expect(css).toMatch(/\.settings-button\s*\{[^}]*min-height:\s*2\.75rem[^}]*font-size:\s*0\.875rem/s)
    expect(css).toMatch(/\.settings-message\s*\{[^}]*font-size:\s*0\.875rem/s)

    const { container } = render(<SettingsView bridge={bridge()} initialConfig={null} />)
    expect(container.querySelector('.settings-form__content')).toContainElement(screen.getByText('服务连接').closest('section'))
    expect(container.querySelector('.settings-form__content')).not.toContainElement(screen.getByRole('button', { name: '保存设置' }))
    const tauri = readFileSync('src-tauri/tauri.conf.json', 'utf8')
    expect(tauri).toMatch(/"label":\s*"settings"[\s\S]*?"width":\s*560[\s\S]*?"height":\s*520/)
  })

  it('blocks save during compatibility loading and resumes only after a ready catalog', async () => {
    let resolvePreview!: (result: FetchPresentationResult) => void
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>(() => new Promise((resolve) => { resolvePreview = resolve })),
      saveConfig: vi.fn(async () => {}), setAdjustmentMode: vi.fn(async () => {}),
    })
    const { container } = render(<SettingsView bridge={api} initialConfig={saved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.fetchPresentation).toHaveBeenCalledTimes(1))
    expect(screen.getByRole('button', { name: '保存设置' })).toBeDisabled()
    fireEvent.submit(container.querySelector('form')!)
    expect(api.saveConfig).not.toHaveBeenCalled()

    resolvePreview({ status: 200, body: preview })
    await waitFor(() => expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    await waitFor(() => expect(api.saveConfig).toHaveBeenCalledTimes(1))
  })

  it.each([
    ['presentation_unsupported', '服务版本不支持可配置字段'],
    ['game_not_supported', '当前游戏不支持可配置字段'],
  ] as const)('keeps save blocked after %s compatibility response', async (code, message) => {
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      fetchPresentation: vi.fn(async () => ({ status: 404 as const, code })),
      saveConfig: vi.fn(async () => {}),
    })
    const { container } = render(<SettingsView bridge={api} initialConfig={saved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(message)
    expect(screen.getByRole('button', { name: '保存设置' })).toBeDisabled()
    fireEvent.submit(container.querySelector('form')!)
    expect(api.saveConfig).not.toHaveBeenCalled()
  })

  it('treats a successful response without a field catalog as non-configurable', async () => {
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      fetchPresentation: vi.fn(async () => ({ status: 200 as const, body: { ...preview, fields: [] } })),
      saveConfig: vi.fn(async () => {}),
    })
    const { container } = render(<SettingsView bridge={api} initialConfig={saved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('当前游戏没有可配置字段')
    expect(screen.getByRole('button', { name: '保存设置' })).toBeDisabled()
    fireEvent.submit(container.querySelector('form')!)
    expect(api.saveConfig).not.toHaveBeenCalled()
  })

  it('loads a presentation after exact player selection with an abort signal', async () => {
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      fetchPresentation: vi.fn(async () => ({ status: 200 as const, body: preview })),
    })
    render(<SettingsView bridge={api} initialConfig={saved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.fetchPresentation).toHaveBeenCalledWith({
      baseUrl: 'https://palbox.test', gameId: 'palworld', userId: 'uid-2',
    }, expect.any(AbortSignal)))
    expect(await screen.findByTestId('identity-header')).toHaveTextContent('Preview Player')
  })

  it('aborts stale preview requests and ignores a late presentation', async () => {
    const signals: AbortSignal[] = []
    let finishOld!: (value: { status: 200; body: Presentation }) => void
    const api = bridge({
      listPlayers: vi.fn(async (url) => [{ user_id: url.includes('other') ? 'uid-3' : 'uid-2', name: 'Player', account_name: '' }]),
      fetchPresentation: vi.fn<DesktopBridge['fetchPresentation']>((_request, signal) => {
        signals.push(signal)
        if (signals.length === 1) return new Promise((resolve) => { finishOld = resolve })
        return Promise.resolve({ status: 200 as const, body: { ...preview, user_id: 'uid-3', identity: { display_name: 'Fresh Player' } } })
      }),
    })
    render(<SettingsView bridge={api} initialConfig={saved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.fetchPresentation).toHaveBeenCalledTimes(1))
    fireEvent.change(screen.getByLabelText('服务地址'), { target: { value: 'https://other.test' } })
    expect(signals[0].aborted).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家').querySelector('option[value="uid-3"]')).toBeInTheDocument())
    fireEvent.change(screen.getByLabelText('玩家'), { target: { value: 'uid-3' } })
    await waitFor(() => expect(api.fetchPresentation).toHaveBeenCalledTimes(2))
    finishOld({ status: 200, body: preview })
    await waitFor(() => expect(screen.getByTestId('identity-header')).toHaveTextContent('Fresh Player'))
    expect(screen.queryByText('Preview Player')).not.toBeInTheDocument()
  })

  it('reports unsupported presentation precisely and disables saving without losing the layout', async () => {
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      fetchPresentation: vi.fn(async () => ({ status: 404 as const, code: 'presentation_unsupported' as const })),
    })
    render(<SettingsView bridge={api} initialConfig={saved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('服务版本不支持可配置字段')
    expect(screen.getByRole('button', { name: '保存设置' })).toBeDisabled()
  })

  it('preserves other profiles and geometry while saving the edited current-game layout', async () => {
    const other = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    other.slots[0] = { primary: 'other.value', fallback: 'other.backup' }
    const config = { ...saved, displayId: 'display-1', x: 17, y: 29, layouts: { ...saved.layouts, other } }
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      fetchPresentation: vi.fn(async () => ({ status: 200 as const, body: preview })),
      saveConfig: vi.fn(async () => {}), setAdjustmentMode: vi.fn(async () => {}),
    })
    render(<SettingsView bridge={api} initialConfig={config} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await screen.findByTestId('identity-header')
    fireEvent.change(screen.getByLabelText('槽位 1 主字段'), { target: { value: 'activity.today' } })
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    await waitFor(() => expect(api.saveConfig).toHaveBeenCalled())
    const persisted = vi.mocked(api.saveConfig).mock.calls[0][0]
    expect(persisted).toMatchObject({ schema: 2, displayId: 'display-1', x: 17, y: 29, layouts: { other } })
    expect('layouts' in persisted && persisted.layouts.palworld.slots[0].primary).toBe('activity.today')
  })

  it('reset changes only the current game draft and preserves all connection and geometry values on save', async () => {
    const custom = cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT)
    custom.slots[0] = { primary: 'activity.today', fallback: 'activity.week' }
    const config = { ...saved, scale: 1.25 as const, locked: false, displayId: 'screen', x: 9, y: 11, layouts: { palworld: custom } }
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      fetchPresentation: vi.fn(async () => ({ status: 200 as const, body: preview })),
      saveConfig: vi.fn(async () => {}), setAdjustmentMode: vi.fn(async () => {}),
    })
    render(<SettingsView bridge={api} initialConfig={config} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await screen.findByTestId('identity-header')
    fireEvent.click(screen.getByRole('button', { name: '恢复当前游戏默认布局' }))
    expect(screen.getByLabelText('服务地址')).toHaveValue('https://palbox.test')
    expect(screen.getByLabelText('玩家')).toHaveValue('uid-2')
    expect(screen.getByLabelText('缩放')).toHaveValue('1.25')
    expect(screen.getByLabelText('锁定并保持鼠标穿透')).not.toBeChecked()
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    await waitFor(() => expect(api.saveConfig).toHaveBeenCalledWith({ ...config, layouts: { palworld: cloneLayoutProfile(PALWORLD_DEFAULT_LAYOUT) } }))
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
    await waitFor(() => expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled())
    fireEvent.change(screen.getByLabelText('缩放'), { target: { value: '1.25' } })
    fireEvent.click(screen.getByRole('checkbox', { name: '锁定并保持鼠标穿透' }))
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    fireEvent.click(screen.getByRole('button', { name: '正在保存…' }))
    expect(api.saveConfig).toHaveBeenCalledTimes(1)
    expect(api.saveConfig).toHaveBeenCalledWith({ ...saved, scale: 1.25, locked: false })
    finish()
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1))
    expect(api.setAdjustmentMode).toHaveBeenCalledWith(true)
    expect(screen.getByText('设置已保存')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '调整悬浮条位置' }))
    expect(api.setAdjustmentMode).toHaveBeenCalledWith(true)
  })

  it('locks native lifecycle before publishing a locked save', async () => {
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      saveConfig: vi.fn(async () => {}), setAdjustmentMode: vi.fn(async () => {}),
    })
    const onSaved = vi.fn()
    render(<SettingsView bridge={api} initialConfig={saved} onSaved={onSaved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid-2'))
    await waitFor(() => expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    await waitFor(() => expect(api.setAdjustmentMode).toHaveBeenCalledWith(false))
    expect(onSaved).toHaveBeenCalledTimes(1)
  })

  it('reports lifecycle sync failure without publishing a completed save', async () => {
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      saveConfig: vi.fn(async () => {}),
      setAdjustmentMode: vi.fn(async () => { throw new Error('native failure') }),
    })
    const onSaved = vi.fn()
    render(<SettingsView bridge={api} initialConfig={saved} onSaved={onSaved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid-2'))
    await waitFor(() => expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('设置已保存，但悬浮条状态同步失败')
    expect(onSaved).not.toHaveBeenCalled()
  })

  it('reports native event sync failure as persisted without leaking details', async () => {
    const api = bridge({
      listPlayers: vi.fn(async () => [{ user_id: 'uid-2', name: 'Player', account_name: '' }]),
      saveConfig: vi.fn(async () => { throw { persisted: true, secret: 'native details' } }),
    })
    render(<SettingsView bridge={api} initialConfig={saved} />)
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(screen.getByLabelText('玩家')).toHaveValue('uid-2'))
    await waitFor(() => expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled())

    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('设置已保存，但悬浮条状态同步失败')
    expect(alert).not.toHaveTextContent(/native details|secret/)
    expect(api.setAdjustmentMode).not.toHaveBeenCalled()
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
    await waitFor(() => expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled())
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
    await waitFor(() => expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled())
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
    expect(api.setAdjustmentMode).not.toHaveBeenCalled()
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
    fireEvent.change(screen.getByLabelText('玩家'), { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: '加载玩家' }))
    await waitFor(() => expect(api.listPlayers).toHaveBeenCalledTimes(5))
    expect(screen.getByLabelText('玩家')).toHaveValue('')
  })
})
