import { useEffect, useRef, useState } from 'react'
import { parseTeamPositions, type TeamPositions } from './teamPositions'
import { detectTeamTeleports, TELEPORT_DISPLAY_MS, MAX_TELEPORTS, type TeamTeleport } from './teamTeleports'

type FetchPositions = (baseUrl: string, signal: AbortSignal) => Promise<unknown>
type State = { key: string; data?: TeamPositions; error?: string; now: number; teleports: TeamTeleport[] }
const INTERVAL = 5000
const FRESH_FOR = 15000

export function useTeamPositions(fetch: FetchPositions | undefined, baseUrl: string, active: boolean) {
  const accepted = useRef<{ key: string; data?: TeamPositions }>({ key: baseUrl })
  const [state, setState] = useState<State>({ key: '', now: Date.now(), teleports: [] })
  useEffect(() => {
    if (!active || !fetch) return
    let stopped = false
    let pollTimer: ReturnType<typeof setTimeout> | undefined
    let timeout: ReturnType<typeof setTimeout> | undefined
    let controller: AbortController | undefined
    if (accepted.current.key !== baseUrl) accepted.current = { key: baseUrl }
    let latest = accepted.current.data
    let failures = 0
    let baseline: TeamPositions | undefined
    let teleports: TeamTeleport[] = []
    const expire = (now: number) => {
      if (teleports.some(t => t.observedAt + TELEPORT_DISPLAY_MS <= now)) teleports = teleports.filter(t => t.observedAt + TELEPORT_DISPLAY_MS > now)
    }
    setState({ key: baseUrl, data: latest, now: Date.now(), teleports })
    const ageTimer = setInterval(() => { expire(Date.now()); setState(s => ({ ...s, now: Date.now(), teleports })) }, INTERVAL)
    async function query() {
      const request = new AbortController()
      controller = request
      timeout = setTimeout(() => {
        baseline = undefined
        request.abort()
        if (!stopped) setState(s => ({ ...s, error: '请求超时，保留最后位置', now: Date.now() }))
      }, 5000)
      try {
        const raw = await fetch!(baseUrl, request.signal)
        if (stopped || request.signal.aborted) return
        const next = parseTeamPositions(raw)
        const nextTime = Date.parse(next.as_of)
        if (baseline && nextTime > Date.parse(baseline.as_of)) {
          const detected = detectTeamTeleports(baseline, next)
          if (detected.length) teleports = [...teleports, ...detected].slice(-MAX_TELEPORTS)
          expire(Date.now())
        }
        baseline = !latest || nextTime > Date.parse(latest.as_of) ? next
          : nextTime === Date.parse(latest.as_of) ? latest : undefined
        if (!latest || Date.parse(next.as_of) > Date.parse(latest.as_of)) {
          const samePlayers = latest && latest.players.length === next.players.length && latest.players.every((p, i) => {
            const n = next.players[i]
            return p.user_id === n.user_id && p.name === n.name && p.x === n.x && p.y === n.y
          })
          latest = samePlayers ? { ...next, players: latest!.players } : next
          accepted.current = { key: baseUrl, data: latest }
        }
        failures = 0
        setState({ key: baseUrl, data: latest, now: Date.now(), teleports })
      } catch {
        baseline = undefined
        if (!stopped) {
          failures++
          setState(s => ({ ...s, error: '连接中断，保留最后位置', now: Date.now() }))
        }
      } finally {
        clearTimeout(timeout)
        // Schedule only after native work settles, never overlap requests.
        if (!stopped) pollTimer = setTimeout(() => void query(), Math.min(INTERVAL * 2 ** Math.min(failures, 3), 30000))
      }
    }
    void query()
    return () => { stopped = true; controller?.abort(); clearTimeout(pollTimer); clearTimeout(timeout); clearInterval(ageTimer) }
  }, [fetch, baseUrl, active])
  const current = state.key === baseUrl ? state : { key: baseUrl, now: Date.now(), teleports: [] }
  const age = current.data ? Math.max(0, current.now - Date.parse(current.data.as_of)) : Infinity
  return { ...current, age, stale: !!current.error || age > FRESH_FOR, unavailable: !fetch }
}
