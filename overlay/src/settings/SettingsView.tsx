import { useEffect, useRef, useState, type FormEvent } from 'react'

import type { DesktopBridge, PlayerListItem } from '../core/bridge'
import { buildOverlayConfig, normalizeBaseUrl, type OverlayConfigV1 } from '../core/config'
import '../styles.css'

export interface SettingsViewProps {
  bridge: DesktopBridge
  initialConfig: OverlayConfigV1 | null
  detectedUserId?: string | null
  platform?: string
  reselectSignal?: number
  onSaved?: (config: OverlayConfigV1) => void
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
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState<{ tone: 'status' | 'error'; text: string } | null>(null)
  const listController = useRef<AbortController | null>(null)
  const mounted = useRef(false)
  const listGeneration = useRef(0)
  const saveGeneration = useRef(0)
  const adjustGeneration = useRef(0)
  const lastReselectSignal = useRef(0)
  const suppressKnownIdentity = useRef(false)
  const explicitlySelectedUserId = useRef<string | null>(null)

  useEffect(() => {
    mounted.current = true
    document.documentElement.classList.add('settings-window')
    document.body.classList.add('settings-window')
    return () => {
      mounted.current = false
      listGeneration.current += 1
      saveGeneration.current += 1
      adjustGeneration.current += 1
      listController.current?.abort()
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
    listController.current = null
    setLoading(false)
    setPlayers([])
    setUserId('')
    setLoadedBaseUrl(null)
  }

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
      displayId: initialConfig?.displayId, x: initialConfig?.x, y: initialConfig?.y,
    })
    if (!config) {
      setMessage({ tone: 'error', text: '设置无效，请检查后重试' })
      return
    }
    setSaving(true)
    setMessage(null)
    const generation = ++saveGeneration.current
    try {
      await bridge.saveConfig(config)
      if (!mounted.current || generation !== saveGeneration.current) return
      setMessage({ tone: 'status', text: '设置已保存' })
      onSaved?.(config)
    } catch {
      if (mounted.current && generation === saveGeneration.current) setMessage({ tone: 'error', text: '保存失败，请稍后重试' })
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

        <footer className="settings-actions">
          {message ? <p role={message.tone === 'error' ? 'alert' : 'status'} className={`settings-message settings-message--${message.tone}`}>{message.text}</p> : <span />}
          <div>
            {initialConfig ? <button className="settings-button settings-button--ghost" type="button" onClick={adjustPosition}>调整悬浮条位置</button> : null}
            <button className="settings-button settings-button--primary" type="submit" disabled={saving || loading}>{saving ? '正在保存…' : '保存设置'}</button>
          </div>
        </footer>
      </form>
    </main>
  )
}
