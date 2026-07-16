import { useEffect, useRef, useState, type FormEvent } from 'react'

import type { DesktopBridge, PlayerListItem } from '../core/bridge'
import { buildOverlayConfig, normalizeBaseUrl, type OverlayConfigV1 } from '../core/config'
import '../styles.css'

export interface SettingsViewProps {
  bridge: DesktopBridge
  initialConfig: OverlayConfigV1 | null
  detectedUserId?: string | null
  platform?: string
  onSaved?: (config: OverlayConfigV1) => void
}

function uniquePlayers(rows: PlayerListItem[]): PlayerListItem[] {
  const seen = new Set<string>()
  const result: PlayerListItem[] = []
  for (const row of rows) {
    const uid = typeof row.user_id === 'string' ? row.user_id.trim() : ''
    if (!uid || seen.has(uid)) continue
    seen.add(uid)
    result.push({ user_id: uid, name: row.name ?? '', account_name: row.account_name ?? '' })
  }
  return result
}

function playerLabel(player: PlayerListItem): string {
  const context = [player.name, player.account_name].filter(Boolean).join(' · ')
  return context ? `${context} — ${player.user_id}` : player.user_id
}

export function SettingsView({ bridge, initialConfig, detectedUserId, platform, onSaved }: SettingsViewProps) {
  const [baseUrl, setBaseUrl] = useState(initialConfig?.baseUrl ?? '')
  const [players, setPlayers] = useState<PlayerListItem[]>([])
  const [userId, setUserId] = useState('')
  const [scale, setScale] = useState(String(initialConfig?.scale ?? 1))
  const [locked, setLocked] = useState(initialConfig?.locked ?? true)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState<{ tone: 'status' | 'error'; text: string } | null>(null)
  const listController = useRef<AbortController | null>(null)

  useEffect(() => {
    document.documentElement.classList.add('settings-window')
    document.body.classList.add('settings-window')
    return () => {
      listController.current?.abort()
      document.documentElement.classList.remove('settings-window')
      document.body.classList.remove('settings-window')
    }
  }, [])

  async function loadPlayers() {
    let normalized: string
    try {
      normalized = normalizeBaseUrl(baseUrl)
    } catch {
      setMessage({ tone: 'error', text: '请输入有效的 HTTP 或 HTTPS 服务地址' })
      return
    }
    listController.current?.abort()
    const controller = new AbortController()
    listController.current = controller
    setLoading(true)
    setMessage(null)
    try {
      const listed = uniquePlayers(await bridge.listPlayers(normalized, controller.signal))
      if (controller.signal.aborted) return
      setBaseUrl(normalized)
      setPlayers(listed)
      const exactSaved = initialConfig && listed.some((player) => player.user_id === initialConfig.userId)
        ? initialConfig.userId : ''
      const exactDetected = platform === 'windows' && detectedUserId && listed.some((player) => player.user_id === detectedUserId)
        ? detectedUserId : ''
      setUserId(exactSaved || exactDetected || '')
      setMessage(listed.length ? null : { tone: 'status', text: '未找到可选择的玩家' })
    } catch {
      if (!controller.signal.aborted) setMessage({ tone: 'error', text: '玩家列表加载失败，请检查服务后重试' })
    } finally {
      if (listController.current === controller) {
        listController.current = null
        setLoading(false)
      }
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (saving) return
    if (!players.some((player) => player.user_id === userId)) {
      setMessage({ tone: 'error', text: '请从已加载的列表中选择玩家' })
      return
    }
    const config = buildOverlayConfig({
      baseUrl, userId, scale, locked,
      displayId: initialConfig?.displayId, x: initialConfig?.x, y: initialConfig?.y,
    })
    if (!config) {
      setMessage({ tone: 'error', text: '设置无效，请检查后重试' })
      return
    }
    setSaving(true)
    setMessage(null)
    try {
      await bridge.saveConfig(config)
      setMessage({ tone: 'status', text: '设置已保存' })
      onSaved?.(config)
    } catch {
      setMessage({ tone: 'error', text: '保存失败，请稍后重试' })
    } finally {
      setSaving(false)
    }
  }

  async function adjustPosition() {
    try {
      await bridge.setAdjustmentMode(true)
      setMessage({ tone: 'status', text: '现在可以拖动悬浮条调整位置' })
    } catch {
      setMessage({ tone: 'error', text: '暂时无法进入位置调整模式' })
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
              <input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="https://palbox.tailnet.ts.net:9443" autoComplete="url" />
            </label>
            <button className="settings-button settings-button--secondary" type="button" aria-label="加载玩家" onClick={loadPlayers}>
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
            <select value={userId} onChange={(event) => setUserId(event.target.value)}>
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
            <button className="settings-button settings-button--primary" type="submit" disabled={saving}>{saving ? '正在保存…' : '保存设置'}</button>
          </div>
        </footer>
      </form>
    </main>
  )
}
