import { useEffect, useRef, useState, type FormEvent } from 'react'

import { configSaveWasPersisted, type DesktopBridge, type PlayerListItem } from '../core/bridge'
import { buildOverlayConfig, normalizeBaseUrl, type OverlayConfigV1 } from '../core/config'
import { parsePresentation, type Presentation } from '../contracts/presentation'
import { cloneLayoutProfile, PALWORLD_DEFAULT_LAYOUT, type LayoutProfile } from '../core/layout'
import { palworldAdapter } from '../games/palworld/adapter'
import { HudLayoutEditor } from './HudLayoutEditor'
import '../styles.css'

export interface SettingsViewProps {
  bridge: DesktopBridge
  initialConfig: OverlayConfigV1 | null
  detectedUserId?: string | null
  platform?: string
  reselectSignal?: number
  onSaved?: (config: OverlayConfigV1) => void
}

type PreviewState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; presentation: Presentation }
  | { status: 'unsupported'; message: string }
  | { status: 'error'; message: string }

function initialLayout(config: OverlayConfigV1 | null): LayoutProfile {
  if (config?.schema === 2) {
    const stored = config.layouts[config.gameId]
    if (stored) return cloneLayoutProfile(stored)
  }
  return palworldAdapter.defaultLayout
}

function uniquePlayers(value: unknown): PlayerListItem[] {
  if (!Array.isArray(value)) throw new Error('invalid player list')
  const seen = new Set<string>()
  const result: PlayerListItem[] = []
  for (const row of value) {
    if (typeof row !== 'object' || row === null || Array.isArray(row)) continue
    const candidate = row as Record<string, unknown>
    if (typeof candidate.user_id !== 'string' || typeof candidate.name !== 'string' ||
        typeof candidate.account_name !== 'string') continue
    const uid = candidate.user_id.trim()
    if (!uid || seen.has(uid)) continue
    seen.add(uid)
    result.push({ user_id: uid, name: candidate.name.trim(), account_name: candidate.account_name.trim() })
  }
  return result
}

function playerLabel(player: PlayerListItem): string {
  const context = [player.name, player.account_name].filter(Boolean).join(' · ')
  return context ? `${context} — ${player.user_id}` : player.user_id
}

export function SettingsView({ bridge, initialConfig, detectedUserId, platform, reselectSignal = 0, onSaved }: SettingsViewProps) {
  const [baseUrl, setBaseUrl] = useState(initialConfig?.baseUrl ?? '')
  const [players, setPlayers] = useState<PlayerListItem[]>([])
  const [userId, setUserId] = useState('')
  const [loadedBaseUrl, setLoadedBaseUrl] = useState<string | null>(null)
  const [scale, setScale] = useState(String(initialConfig?.scale ?? 1))
  const [locked, setLocked] = useState(initialConfig?.locked ?? true)
  const [layout, setLayout] = useState(() => initialLayout(initialConfig))
  const [preview, setPreview] = useState<PreviewState>({ status: 'idle' })
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState<{ tone: 'status' | 'error'; text: string } | null>(null)
  const listController = useRef<AbortController | null>(null)
  const previewController = useRef<AbortController | null>(null)
  const mounted = useRef(false)
  const listGeneration = useRef(0)
  const previewGeneration = useRef(0)
  const saveGeneration = useRef(0)
  const adjustGeneration = useRef(0)
  const lastReselectSignal = useRef(0)
  const suppressKnownIdentity = useRef(false)
  const explicitlySelectedUserId = useRef<string | null>(null)
  const previewSelectionIsExact = loadedBaseUrl !== null && userId !== '' &&
    players.some((player) => player.user_id === userId)
  const previewMatchesSelection = preview.status === 'ready' &&
    preview.presentation.game_id === (initialConfig?.gameId ?? 'palworld') &&
    preview.presentation.user_id === userId
  const layoutSaveBlocked = previewSelectionIsExact && !previewMatchesSelection

  useEffect(() => {
    mounted.current = true
    document.documentElement.classList.add('settings-window')
    document.body.classList.add('settings-window')
    return () => {
      mounted.current = false
      listGeneration.current += 1
      previewGeneration.current += 1
      saveGeneration.current += 1
      adjustGeneration.current += 1
      listController.current?.abort()
      previewController.current?.abort()
      document.documentElement.classList.remove('settings-window')
      document.body.classList.remove('settings-window')
    }
  }, [])

  function changeBaseUrl(value: string) {
    setBaseUrl(value)
    let normalized: string | null = null
    try {
      normalized = normalizeBaseUrl(value)
    } catch {
      // An invalid edit always invalidates the list tied to the previous service.
    }
    if (normalized === loadedBaseUrl) return
    listGeneration.current += 1
    listController.current?.abort()
    previewGeneration.current += 1
    previewController.current?.abort()
    previewController.current = null
    setPreview({ status: 'idle' })
    listController.current = null
    setLoading(false)
    setPlayers([])
    setUserId('')
    setLoadedBaseUrl(null)
  }

  useEffect(() => {
    previewGeneration.current += 1
    previewController.current?.abort()
    previewController.current = null
    if (!loadedBaseUrl || !userId || !players.some((player) => player.user_id === userId)) {
      setPreview({ status: 'idle' })
      return
    }

    const controller = new AbortController()
    const generation = previewGeneration.current
    previewController.current = controller
    setPreview({ status: 'loading' })
    void bridge.fetchPresentation({
      baseUrl: loadedBaseUrl,
      gameId: initialConfig?.gameId ?? 'palworld',
      userId,
    }, controller.signal).then((result) => {
      if (!mounted.current || controller.signal.aborted || generation !== previewGeneration.current) return
      if (result.status === 200) {
        try {
          const presentation = parsePresentation(result.body)
          if (presentation.game_id !== (initialConfig?.gameId ?? 'palworld') || presentation.user_id !== userId) {
            throw new Error('presentation identity mismatch')
          }
          if (presentation.fields.length === 0) {
            setPreview({ status: 'unsupported', message: '当前游戏没有可配置字段' })
            return
          }
          setPreview({ status: 'ready', presentation })
        } catch {
          setPreview({ status: 'error', message: 'HUD 预览数据不兼容，请更新应用或服务' })
        }
        return
      }
      if (result.status === 404 && result.code === 'presentation_unsupported') {
        setPreview({ status: 'unsupported', message: '服务版本不支持可配置字段' })
      } else if (result.status === 404 && result.code === 'game_not_supported') {
        setPreview({ status: 'unsupported', message: '当前游戏不支持可配置字段' })
      } else if (result.status === 404 && result.code === 'player_not_found') {
        setPreview({ status: 'error', message: '所选玩家不存在，请重新加载玩家' })
      } else if (result.status === 404) {
        setPreview({ status: 'error', message: '当前游戏不支持可配置字段' })
      } else {
        setPreview({ status: 'error', message: '暂时无法加载 HUD 预览，请稍后重试' })
      }
    }).catch(() => {
      if (mounted.current && !controller.signal.aborted && generation === previewGeneration.current) {
        setPreview({ status: 'error', message: 'HUD 预览加载失败，请检查服务后重试' })
      }
    }).finally(() => {
      if (previewController.current === controller) previewController.current = null
    })
    return () => controller.abort()
  }, [bridge, initialConfig?.gameId, loadedBaseUrl, players, userId])

  async function loadPlayers() {
    listController.current?.abort()
    const generation = ++listGeneration.current
    setLoadedBaseUrl(null)
    setPlayers([])
    setUserId('')
    setLoading(false)
    let normalized: string
    try {
      normalized = normalizeBaseUrl(baseUrl)
    } catch {
      setMessage({ tone: 'error', text: '请输入有效的 HTTP 或 HTTPS 服务地址' })
      return
    }
    const controller = new AbortController()
    listController.current = controller
    setLoading(true)
    setMessage(null)
    try {
      const response: unknown = await bridge.listPlayers(normalized, controller.signal)
      const listed = uniquePlayers(response)
      if (!mounted.current || generation !== listGeneration.current || controller.signal.aborted) return
      setBaseUrl(normalized)
      setPlayers(listed)
      setLoadedBaseUrl(normalized)
      const exactExplicit = explicitlySelectedUserId.current && listed.some((player) => player.user_id === explicitlySelectedUserId.current)
        ? explicitlySelectedUserId.current : ''
      const exactSaved = !suppressKnownIdentity.current && initialConfig && listed.some((player) => player.user_id === initialConfig.userId)
        ? initialConfig.userId : ''
      const exactDetected = !suppressKnownIdentity.current && platform === 'windows' && detectedUserId && listed.some((player) => player.user_id === detectedUserId)
        ? detectedUserId : ''
      setUserId(exactExplicit || exactSaved || exactDetected || '')
      setMessage(listed.length ? null : { tone: 'status', text: '未找到可选择的玩家' })
    } catch {
      if (mounted.current && generation === listGeneration.current && !controller.signal.aborted) {
        setMessage({ tone: 'error', text: '玩家列表加载失败，请检查服务后重试' })
      }
    } finally {
      if (mounted.current && generation === listGeneration.current && listController.current === controller) {
        listController.current = null
        setLoading(false)
      }
    }
  }

  useEffect(() => {
    if (lastReselectSignal.current === reselectSignal) return
    lastReselectSignal.current = reselectSignal
    suppressKnownIdentity.current = true
    explicitlySelectedUserId.current = null
    void loadPlayers()
  }, [reselectSignal])

  async function save(event: FormEvent) {
    event.preventDefault()
    if (saving) return
    if (layoutSaveBlocked) {
      const text = preview.status === 'loading' || preview.status === 'idle'
        ? '正在验证服务是否支持可配置字段，请稍候'
        : preview.status === 'ready'
          ? 'HUD 预览与当前选择不匹配，请稍候'
          : preview.message
      setMessage({ tone: 'error', text })
      return
    }
    let normalized: string
    try {
      normalized = normalizeBaseUrl(baseUrl)
    } catch {
      setMessage({ tone: 'error', text: '请输入有效的 HTTP 或 HTTPS 服务地址' })
      return
    }
    if (loadedBaseUrl !== normalized) {
      setMessage({ tone: 'error', text: '服务地址已更改，请重新加载玩家' })
      return
    }
    if (!players.some((player) => player.user_id === userId)) {
      setMessage({ tone: 'error', text: '请从已加载的列表中选择玩家' })
      return
    }
    const config = buildOverlayConfig({
      baseUrl: normalized, userId, scale, locked,
      gameId: initialConfig?.gameId ?? 'palworld',
      layout,
      layouts: initialConfig?.schema === 2 ? initialConfig.layouts : undefined,
      displayId: initialConfig?.displayId, x: initialConfig?.x, y: initialConfig?.y,
    })
    if (!config) {
      setMessage({ tone: 'error', text: '设置无效，请检查后重试' })
      return
    }
    setSaving(true)
    setMessage(null)
    const generation = ++saveGeneration.current
    let persisted = false
    try {
      await bridge.saveConfig(config)
      persisted = true
      if (!mounted.current || generation !== saveGeneration.current) return
      await bridge.setAdjustmentMode(!config.locked)
      if (!mounted.current || generation !== saveGeneration.current) return
      setMessage({ tone: 'status', text: '设置已保存' })
      onSaved?.(config)
    } catch (error) {
      if (configSaveWasPersisted(error)) persisted = true
      if (mounted.current && generation === saveGeneration.current) {
        setMessage({
          tone: 'error',
          text: persisted ? '设置已保存，但悬浮条状态同步失败' : '保存失败，请稍后重试',
        })
      }
    } finally {
      if (mounted.current && generation === saveGeneration.current) setSaving(false)
    }
  }

  async function adjustPosition() {
    const generation = ++adjustGeneration.current
    try {
      await bridge.setAdjustmentMode(true)
      if (!mounted.current || generation !== adjustGeneration.current) return
      setMessage({ tone: 'status', text: '现在可以拖动悬浮条调整位置' })
    } catch {
      if (mounted.current && generation === adjustGeneration.current) setMessage({ tone: 'error', text: '暂时无法进入位置调整模式' })
    }
  }

  return (
    <main className="settings-shell">
      <header className="settings-header">
        <span className="settings-kicker">PALREST · GAME LINK</span>
        <h1>悬浮条设置</h1>
        <p>连接私网服务，并明确选择此设备要显示的玩家。</p>
      </header>

      <form className="settings-form" onSubmit={save}>
        <div className="settings-form__content">
          <section className="settings-group" aria-labelledby="connection-title">
            <div className="settings-group__title">
              <span>01</span><h2 id="connection-title">服务连接</h2>
            </div>
            <div className="settings-inline">
              <label className="settings-field settings-field--grow">
                <span>服务地址</span>
                <input value={baseUrl} onChange={(event) => changeBaseUrl(event.target.value)} placeholder="https://palbox.tailnet.ts.net:9443" autoComplete="url" />
              </label>
              <button className="settings-button settings-button--secondary" type="button" aria-label="加载玩家" onClick={() => { void loadPlayers() }}>
                {loading ? '正在加载…' : '加载玩家'}
              </button>
            </div>
            <label className="settings-field">
              <span>游戏</span>
              <input value="幻兽帕鲁 / Palworld" disabled />
            </label>
          </section>

          <section className="settings-group" aria-labelledby="player-title">
            <div className="settings-group__title">
              <span>02</span><h2 id="player-title">显示对象</h2>
            </div>
            <label className="settings-field">
              <span>玩家</span>
              <select value={userId} onChange={(event) => {
                const nextUserId = event.target.value
                if (nextUserId) {
                  suppressKnownIdentity.current = false
                  explicitlySelectedUserId.current = nextUserId
                } else {
                  suppressKnownIdentity.current = true
                  explicitlySelectedUserId.current = null
                }
                setUserId(nextUserId)
              }}>
                <option value="">请选择精确玩家 UID</option>
                {players.map((player) => <option key={player.user_id} value={player.user_id}>{playerLabel(player)}</option>)}
              </select>
            </label>
            <div className="settings-options">
              <label className="settings-field">
                <span>缩放</span>
                <select value={scale} onChange={(event) => setScale(event.target.value)}>
                  <option value="0.8">80%</option><option value="1">100%</option><option value="1.25">125%</option>
                </select>
              </label>
              <label className="settings-check"><input type="checkbox" checked={locked} onChange={(event) => setLocked(event.target.checked)} /><span>锁定并保持鼠标穿透</span></label>
            </div>
          </section>

          <section className="settings-group settings-group--hud" aria-labelledby="hud-title">
            <div className="settings-group__title">
              <span>03</span><h2 id="hud-title">HUD 布局</h2>
            </div>
            {!userId ? <p className="hud-editor__state">选择精确玩家后即可加载字段目录和实时预览。</p> : null}
            {preview.status === 'loading' ? <p className="hud-editor__state" role="status">正在加载 HUD 预览…</p> : null}
            {preview.status === 'unsupported' ? (
              <p className="hud-editor__state hud-editor__state--error" role="alert">{preview.message}</p>
            ) : null}
            {preview.status === 'error' ? (
              <p className="hud-editor__state hud-editor__state--error" aria-live="polite">{preview.message}</p>
            ) : null}
            {preview.status === 'ready' ? <HudLayoutEditor
              presentation={preview.presentation}
              layout={layout}
              defaultLayout={PALWORLD_DEFAULT_LAYOUT}
              mapBaseUrl={loadedBaseUrl ?? undefined}
              onChange={setLayout}
            /> : null}
          </section>
        </div>

        <footer className="settings-actions">
          {message ? <p role={message.tone === 'error' ? 'alert' : 'status'} className={`settings-message settings-message--${message.tone}`}>{message.text}</p> : <span />}
          <div>
            {initialConfig ? <button className="settings-button settings-button--ghost" type="button" onClick={adjustPosition}>调整悬浮条位置</button> : null}
            <button className="settings-button settings-button--primary" type="submit" disabled={saving || loading || layoutSaveBlocked}>{saving ? '正在保存…' : '保存设置'}</button>
          </div>
        </footer>
      </form>
    </main>
  )
}
