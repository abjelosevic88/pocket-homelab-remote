// /connect/:id — a computer's screen (or terminal), full screen, with a touch bar on phones.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import Guacamole from 'guacamole-common-js'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { KEY, PROTOCOL_NAME, kindIcon } from '../lib/remote'
import type { Machine } from '../lib/types'
import { Icon } from '../components/icons'
import { FileBrowser } from '../components/FileBrowser'
import { Confirm, Menu, type Anchor, type MenuItem, Modal, copyText, rectOf, useIsMobile, useToast } from '../components/ui'

type Phase = 'starting' | 'pin' | 'connecting' | 'connected' | 'ended'

// Client states and the tunnel's "unstable".
const CONNECTED = 3, DISCONNECTED = 5, UNSTABLE = 3

/** Plain words for the Guacamole status codes. */
function statusText(code: number, msg: string | undefined, m: Machine | null): string {
  const what = m ? PROTOCOL_NAME[m.protocol] : 'remote access'
  switch (code) {
    case 519: return msg && !/^Aborted|^Connection failed/i.test(msg) ? msg : `${m?.name || 'The computer'} did not answer. Is it on, and is ${what} switched on?`
    case 769: case 771: return msg || 'The computer did not accept the sign-in. Check the user name and password (Edit).'
    case 514: case 776: return 'The connection timed out.'
    case 520: return `${m?.name || 'The computer'} is busy or refused the session.`
    case 521: return 'Someone else took over this session.'
    case 522: case 523: return 'The session ended on the computer.'
  }
  return msg || 'The connection failed.'
}

/** Sends text as key presses (for login screens without a clipboard). */
function typeText(client: Guacamole.Client, text: string) {
  for (const ch of text) {
    const c = ch.codePointAt(0)!
    const ks = ch === '\n' ? KEY.enter : ch === '\t' ? KEY.tab : c < 0x100 ? c : 0x01000000 | c
    client.sendKeyEvent(1, ks)
    client.sendKeyEvent(0, ks)
  }
}

export default function Connect() {
  const { id } = useParams()
  const [sp] = useSearchParams()
  const way = sp.get('way') === 'terminal' ? 'terminal' : sp.get('way') === 'desktop' ? 'desktop' : ''
  const navigate = useNavigate()
  const { settings } = useApp()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const touch = typeof window !== 'undefined' && ('ontouchstart' in window || navigator.maxTouchPoints > 0)

  const [m, setM] = useState<Machine | null>(null)
  const [phase, setPhase] = useState<Phase>('starting')
  const [error, setError] = useState('')
  const [pin, setPin] = useState('')
  const [pinErr, setPinErr] = useState('')
  const [unstable, setUnstable] = useState(false)
  const [mode, setMode] = useState<'trackpad' | 'touch'>(settings?.touch_mode || 'trackpad')
  const [zoom, setZoom] = useState<'fit' | number>(settings?.scale_mode === 'actual' ? 1 : 'fit')
  const [keys, setKeys] = useState(!!settings?.keys_bar)
  const [latched, setLatched] = useState<number[]>([])
  const [remoteClip, setRemoteClip] = useState('')
  const [sheet, setSheet] = useState<'clip' | 'files' | null>(null)
  const [menu, setMenu] = useState<Anchor | null>(null)
  const [leave, setLeave] = useState(false)
  const [barShown, setBarShown] = useState(true)
  const [full, setFull] = useState(false)

  const hostRef = useRef<HTMLDivElement>(null)
  const screenRef = useRef<HTMLDivElement>(null)
  const clientRef = useRef<Guacamole.Client | null>(null)
  const sinkRef = useRef<Guacamole.InputSink | null>(null)
  const kbRef = useRef<Guacamole.Keyboard | null>(null)
  const modeRef = useRef(mode); modeRef.current = mode
  const zoomRef = useRef(zoom); zoomRef.current = zoom
  const latchedRef = useRef(latched); latchedRef.current = latched
  const heldMods = useRef<number[]>([])
  const pressed = useRef(new Set<number>())
  const phaseRef = useRef(phase); phaseRef.current = phase

  // settings can arrive just after the first render (a direct link to a session)
  const synced = useRef(false)
  useEffect(() => {
    if (!settings || synced.current) return
    synced.current = true
    setMode(settings.touch_mode); setZoom(settings.scale_mode === 'actual' ? 1 : 'fit'); setKeys(settings.keys_bar)
  }, [settings])
  // a phone held upright: say once that sideways shows more
  const [portrait, setPortrait] = useState(false)
  useEffect(() => {
    if (!touch || phase !== 'connected' || sessionStorage.getItem('prm-turn')) return
    const mq = window.matchMedia('(orientation: portrait)')
    setPortrait(mq.matches)
    const h = () => setPortrait(mq.matches)
    mq.addEventListener('change', h)
    const t = setTimeout(() => { setPortrait(false); sessionStorage.setItem('prm-turn', '1') }, 5000)
    return () => { mq.removeEventListener('change', h); clearTimeout(t) }
  }, [touch, phase])

  // the session's view of the computer: its terminal is "ssh"
  useEffect(() => { api.get<Machine>(`${V1}/machines/${id}`).then(x => {
    const w = way || x.available_ways[0] || (x.desktop_enabled ? 'desktop' : 'terminal')
    setM(w === 'terminal' ? { ...x, protocol: 'ssh', port: x.ssh_port, effective_port: x.ssh_port } : x)
  }).catch(e => { setError((e as Error).message); setPhase('ended') }) }, [id, way])

  const rescale = useCallback(() => {
    const c = clientRef.current, host = hostRef.current
    if (!c || !host) return
    const d = c.getDisplay()
    const dw = d.getWidth(), dh = d.getHeight()
    if (!dw || !dh) return
    const z = zoomRef.current
    d.scale(z === 'fit' ? Math.min(host.clientWidth / dw, host.clientHeight / dh) : z)
  }, [])
  useEffect(rescale, [zoom, rescale])

  const teardown = useCallback(() => {
    const kb = kbRef.current
    if (kb) { kb.onkeydown = null; kb.onkeyup = null; kb.reset() }
    clientRef.current?.disconnect()
    clientRef.current = null
    sinkRef.current?.getElement().remove()
    sinkRef.current = null
    if (screenRef.current) screenRef.current.innerHTML = ''
  }, [])

  const connect = useCallback(async (withPin?: string) => {
    if (!m) return
    teardown()
    setError(''); setPinErr('')
    let r: { ok: boolean; ticket?: string; need_pin?: boolean; error?: string }
    const body: Record<string, string> = { way: m.protocol === 'ssh' ? 'terminal' : 'desktop' }
    if (withPin) body.pin = withPin
    try { r = await api.post(`${V1}/machines/${m.id}/connect`, body) } catch (e) { setError((e as Error).message); setPhase('ended'); return }
    if (r.need_pin) { setPhase('pin'); setPinErr(r.error || ''); return }
    if (!r.ok || !r.ticket) { setError(r.error || 'Could not connect.'); setPhase('ended'); return }
    setPhase('connecting')
    const tunnel = new Guacamole.WebSocketTunnel(`${V1}/tunnel`)
    const client = new Guacamole.Client(tunnel)
    clientRef.current = client
    const display = client.getDisplay()
    const el = display.getElement()
    el.classList.add('rs-display')
    screenRef.current!.innerHTML = ''
    screenRef.current!.appendChild(el)

    // the pointer: a real mouse on computers, a trackpad or direct touch on phones
    const follow = (x: number, y: number) => {
      const host = hostRef.current
      if (!host || zoomRef.current === 'fit') return
      const s = display.getScale(), px = x * s, py = y * s, pad = 60
      if (px < host.scrollLeft + pad) host.scrollLeft = px - pad
      else if (px > host.scrollLeft + host.clientWidth - pad) host.scrollLeft = px - host.clientWidth + pad
      if (py < host.scrollTop + pad) host.scrollTop = py - pad
      else if (py > host.scrollTop + host.clientHeight - pad) host.scrollTop = py - host.clientHeight + pad
    }
    const send = (e: Guacamole.MouseEvent) => { client.sendMouseState(e.state, true); follow(e.state.x / display.getScale(), e.state.y / display.getScale()) }
    if (touch) {
      const pad = new Guacamole.Mouse.Touchpad(el), scr = new Guacamole.Mouse.Touchscreen(el)
      pad.onEach(['mousedown', 'mousemove', 'mouseup'], e => { if (modeRef.current === 'trackpad') send(e) })
      scr.onEach(['mousedown', 'mousemove', 'mouseup'], e => { if (modeRef.current === 'touch') send(e) })
      display.showCursor(true)
    }
    const mouse = new Guacamole.Mouse(el)
    mouse.onEach(['mousedown', 'mousemove', 'mouseup'], e => client.sendMouseState(e.state, true))
    if (!touch) {
      display.showCursor(false)
      display.oncursor = (canvas, x, y) => { el.style.cursor = `url(${canvas.toDataURL()}) ${x} ${y}, default` }
    }

    // the keyboard: the page's own (computers) and an invisible field that opens the phone's
    const sink = new Guacamole.InputSink()
    document.body.appendChild(sink.getElement())
    sinkRef.current = sink
    const kb = kbRef.current || new Guacamole.Keyboard(document)
    kbRef.current = kb
    kb.listenTo(sink.getElement())
    const uiFocused = () => {
      const a = document.activeElement as HTMLElement | null
      return (!!a && a !== sink.getElement() && !!a.closest('input, textarea, select, [contenteditable="true"]')) || !!document.querySelector('.modal-backdrop, .menu')
    }
    kb.onkeydown = ks => {
      if (uiFocused()) return true
      pressed.current.add(ks)
      const p = pressed.current
      if ((p.has(KEY.ctrl) || p.has(0xffe4)) && (p.has(KEY.alt) || p.has(0xffea)) && (p.has(KEY.shift) || p.has(0xffe2))) {
        for (const k of p) client.sendKeyEvent(0, k)
        p.clear(); kb.reset()
        setBarShown(true); setMenu({ x: window.innerWidth / 2 - 120, y: 48, w: 0, h: 0 })
        return false
      }
      if (latchedRef.current.length) { heldMods.current = latchedRef.current; heldMods.current.forEach(k => client.sendKeyEvent(1, k)); setLatched([]) }
      client.sendKeyEvent(1, ks)
      return false
    }
    kb.onkeyup = ks => {
      if (uiFocused()) return
      pressed.current.delete(ks)
      client.sendKeyEvent(0, ks)
      if (heldMods.current.length) { [...heldMods.current].reverse().forEach(k => client.sendKeyEvent(0, k)); heldMods.current = [] }
    }

    client.onclipboard = (stream, mime) => {
      if (!/^text\//.test(mime)) return
      const rd = new Guacamole.StringReader(stream)
      let t = ''
      rd.ontext = x => { t += x }
      rd.onend = () => setRemoteClip(t)
    }
    client.onstatechange = s => {
      if (s === CONNECTED) { setPhase('connected'); rescale(); setTimeout(() => setBarShown(false), 2500) }
      if (s === DISCONNECTED && phaseRef.current !== 'ended') setPhase('ended')
    }
    client.onerror = st => { setError(statusText(st.code, st.message, m)); setPhase('ended') }
    tunnel.onstatechange = s => setUnstable(s === UNSTABLE)
    tunnel.onerror = st => { setError(statusText(st.code, st.message, m)); setPhase('ended') }
    display.onresize = () => rescale()

    const host = hostRef.current!
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    const portrait = host.clientHeight > host.clientWidth
    // a phone held upright still gets a landscape desktop; a computer gets its window's size
    const w = touch && portrait ? 1280 : Math.round(host.clientWidth * (m.protocol === 'ssh' ? 1 : dpr))
    const h = touch && portrait ? 800 : Math.round(host.clientHeight * (m.protocol === 'ssh' ? 1 : dpr))
    const q = new URLSearchParams({ ticket: r.ticket, width: String(w), height: String(h), dpi: String(Math.round(96 * (m.protocol === 'ssh' || (touch && portrait) ? 1 : dpr))), timezone: Intl.DateTimeFormat().resolvedOptions().timeZone })
    client.connect(q.toString())
  }, [m, teardown, touch, rescale])

  useEffect(() => { if (m && phase === 'starting') connect() }, [m]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => () => teardown(), [teardown])

  // follow the window: rescale, and ask Remote Desktop for the new size
  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let t = 0
    const ro = new ResizeObserver(() => {
      rescale()
      window.clearTimeout(t)
      t = window.setTimeout(() => { if (m?.protocol === 'rdp' && m.options.resize && !touch && clientRef.current) { const dpr = Math.min(window.devicePixelRatio || 1, 2); clientRef.current.sendSize(Math.round(host.clientWidth * dpr), Math.round(host.clientHeight * dpr)) } }, 400)
    })
    ro.observe(host)
    return () => { ro.disconnect(); window.clearTimeout(t) }
  }, [m, rescale, touch])

  // keep the screen on while connected; ask before closing the tab
  useEffect(() => {
    if (phase !== 'connected') return
    let lock: { release: () => Promise<void> } | null = null
    const nav = navigator as Navigator & { wakeLock?: { request: (t: string) => Promise<{ release: () => Promise<void> }> } }
    nav.wakeLock?.request('screen').then(l => { lock = l }).catch(() => {})
    const before = (e: BeforeUnloadEvent) => { if (settings?.confirm_close) { e.preventDefault(); e.returnValue = '' } }
    window.addEventListener('beforeunload', before)
    return () => { lock?.release().catch(() => {}); window.removeEventListener('beforeunload', before) }
  }, [phase, settings?.confirm_close])

  // the desktop toolbar shows itself when the pointer nears the top
  useEffect(() => {
    if (touch) return
    const h = (e: MouseEvent) => { if (e.clientY < 12) setBarShown(true) }
    window.addEventListener('mousemove', h)
    return () => window.removeEventListener('mousemove', h)
  }, [touch])

  useEffect(() => {
    const h = () => setFull(!!document.fullscreenElement)
    document.addEventListener('fullscreenchange', h)
    return () => document.removeEventListener('fullscreenchange', h)
  }, [])

  const c = clientRef.current
  const tap = (ks: number) => {
    if (!c) return
    const mods = latched
    mods.forEach(k => c.sendKeyEvent(1, k))
    c.sendKeyEvent(1, ks); c.sendKeyEvent(0, ks)
    ;[...mods].reverse().forEach(k => c.sendKeyEvent(0, k))
    setLatched([])
  }
  const latch = (ks: number) => setLatched(l => l.includes(ks) ? l.filter(x => x !== ks) : [...l, ks])
  const cad = () => { if (!c) return; [KEY.ctrl, KEY.alt, KEY.del].forEach(k => c.sendKeyEvent(1, k)); [KEY.del, KEY.alt, KEY.ctrl].forEach(k => c.sendKeyEvent(0, k)); toast('Ctrl + Alt + Delete sent') }
  const sendClip = (text: string) => {
    if (!c) return
    const w = new Guacamole.StringWriter(c.createClipboardStream('text/plain'))
    w.sendText(text); w.sendEnd()
    toast('Copied to the computer: paste it there')
  }
  const fullscreen = () => { if (document.fullscreenElement) document.exitFullscreen(); else document.documentElement.requestFullscreen?.().catch(() => toast('This browser cannot go full screen here', 'err')) }
  const end = () => { teardown(); navigate('/') }
  const askEnd = () => { if (phase === 'connected' && settings?.confirm_close) setLeave(true); else end() }
  const showKeyboard = () => { sinkRef.current?.focus() }
  const zoomStep = (dir: 1 | -1) => {
    const d = c?.getDisplay(), host = hostRef.current
    if (!d || !host) return
    const fit = Math.min(host.clientWidth / (d.getWidth() || 1), host.clientHeight / (d.getHeight() || 1))
    const cur = zoom === 'fit' ? fit : zoom
    const next = Math.max(fit, Math.min(3, cur * (dir > 0 ? 1.4 : 1 / 1.4)))
    setZoom(Math.abs(next - fit) < 0.01 ? 'fit' : next)
  }
  // switch between the sharp and the standard-size desktop: saved for this
  // computer, then the session reconnects at the new size
  const setResolution = async (standard: boolean) => {
    if (!m || !!m.options.standard_res === standard) return
    try {
      const saved = await api.put<Machine>(`${V1}/machines/${m.id}`, { options: { ...m.options, standard_res: standard } })
      setM(saved)
      toast(standard ? 'Standard size: reconnecting…' : 'Sharp: reconnecting…')
    } catch (e) { toast((e as Error).message, 'err') }
  }
  // null until the computer is loaded; "off" arrives as a missing field, so compare as true/false
  const resRef = useRef<boolean | null>(null)
  useEffect(() => {
    if (!m) return
    const standard = !!m.options.standard_res
    if (resRef.current !== null && resRef.current !== standard && phaseRef.current !== 'starting') connect()
    resRef.current = standard
  }, [m]) // eslint-disable-line react-hooks/exhaustive-deps
  const cmd = m?.kind === 'mac' ? KEY.meta : KEY.super

  const moreItems = (): MenuItem[] => [
    { label: 'Clipboard and typing…', icon: 'clipboard', onSelect: () => setSheet('clip') },
    ...(m?.files_enabled ? [{ label: 'Files…', icon: 'folder', onSelect: () => setSheet('files') }] : []),
    ...(m?.protocol !== 'ssh' ? [{ label: 'Ctrl + Alt + Delete', icon: 'keyboard', onSelect: cad }] : []),
    'sep',
    ...(m?.protocol === 'rdp' && (window.devicePixelRatio || 1) > 1 ? [
      { label: 'Sharp (every screen pixel)', icon: 'zoom-in', checked: !m.options.standard_res, onSelect: () => setResolution(false) },
      { label: 'Standard size (as at first)', icon: 'zoom-out', checked: !!m.options.standard_res, onSelect: () => setResolution(true) },
      'sep' as const,
    ] : []),
    { label: 'Fit the screen', icon: 'minimize', checked: zoom === 'fit', onSelect: () => setZoom('fit') },
    { label: 'Actual size', icon: 'maximize', checked: zoom === 1, onSelect: () => setZoom(1) },
    ...(touch ? [{ label: mode === 'trackpad' ? 'Touch to click (direct)' : 'Use as a trackpad', icon: mode === 'trackpad' ? 'hand' : 'pointer', onSelect: () => setMode(mode === 'trackpad' ? 'touch' : 'trackpad') }] : []),
    { label: full ? 'Leave full screen' : 'Full screen', icon: full ? 'minimize' : 'maximize', onSelect: fullscreen },
    'sep',
    { label: 'Reconnect', icon: 'refresh', onSelect: () => connect() },
    { label: 'End the session', icon: 'x', danger: true, onSelect: askEnd },
  ]

  return (
    <div className={`rs ${touch ? 'is-touch' : ''} ${phase}`}>
      {/* desktop: a slim bar at the top that hides itself */}
      {!mobile && (
        <div className={`rs-top ${barShown || phase !== 'connected' ? 'shown' : ''}`} onMouseLeave={() => { if (phase === 'connected' && !menu) setBarShown(false) }}>
          <button type="button" className="btn ghost sm" onClick={askEnd} title="End the session"><Icon name="chevron-left" size={15} />End</button>
          <span className="rs-name">{m && <Icon name={kindIcon(m.kind)} size={15} />}<b className="ellipsis">{m?.name || '…'}</b><span className={`rs-dot ${phase === 'connected' ? (unstable ? 'warn' : 'ok') : ''}`} title={unstable ? 'The connection is slow' : ''} /></span>
          <span className="grow" />
          {m?.protocol !== 'ssh' && <button type="button" className="btn ghost sm" onClick={cad} disabled={phase !== 'connected'} title="Ctrl + Alt + Delete">Ctrl+Alt+Del</button>}
          <button type="button" className="btn ghost icon-only sm" onClick={() => setSheet('clip')} disabled={phase !== 'connected'} title="Clipboard and typing" aria-label="Clipboard"><Icon name="clipboard" size={16} /></button>
          {m?.files_enabled && <button type="button" className="btn ghost icon-only sm" onClick={() => setSheet('files')} title="Files" aria-label="Files"><Icon name="folder" size={16} /></button>}
          <button type="button" className="btn ghost sm" onClick={() => setZoom(zoom === 'fit' ? 1 : 'fit')} title="Fit or actual size">{zoom === 'fit' ? 'Fit' : `${Math.round((zoom as number) * 100)}%`}</button>
          <button type="button" className="btn ghost icon-only sm" onClick={fullscreen} title="Full screen" aria-label="Full screen"><Icon name={full ? 'minimize' : 'maximize'} size={16} /></button>
          <button type="button" className="btn ghost icon-only sm" onClick={e => setMenu(rectOf(e.currentTarget))} title="More (Ctrl+Alt+Shift)" aria-label="More"><Icon name="more" size={16} /></button>
        </div>
      )}

      <div ref={hostRef} className={`rs-host ${zoom === 'fit' ? 'fit' : 'zoomed'}`} onClick={() => { if (touch && phase === 'connected') sinkRef.current?.getElement().blur() }}>
        <div ref={screenRef} className="rs-screen" />
      </div>

      {phase !== 'connected' && (
        <div className="rs-overlay">
          <div className="rs-card">
            {m && <span className={`mc-ico k-${m.kind}`}><Icon name={kindIcon(m.kind)} size={24} /></span>}
            <h2>{m?.name || 'Connecting'}</h2>
            {(phase === 'starting' || phase === 'connecting') && <><p className="muted">{phase === 'connecting' ? `Opening ${m ? PROTOCOL_NAME[m.protocol] : ''}…` : 'Getting ready…'}</p><span className="spinner" /></>}
            {phase === 'pin' && <form className="rs-pin" onSubmit={e => { e.preventDefault(); connect(pin); setPin('') }}>
              <p className="muted">Type your connect PIN.</p>
              <input type="password" inputMode="numeric" autoComplete="off" autoFocus value={pin} onChange={e => setPin(e.target.value.replace(/\D/g, ''))} maxLength={12} aria-label="PIN" className="rs-pin-input" />
              {pinErr && <p className="bad small">{pinErr}</p>}
              <div className="row center"><button type="button" className="btn" onClick={() => navigate('/')}>Cancel</button><button className="btn primary" disabled={pin.length < 4}>Connect</button></div>
            </form>}
            {phase === 'ended' && <>
              <p className={error ? 'bad' : 'muted'}>{error || 'The session ended.'}</p>
              <div className="row center wrap"><button type="button" className="btn" onClick={() => navigate('/')}>Back to computers</button>{m && <button type="button" className="btn primary" onClick={() => connect()}><Icon name="refresh" size={14} />Connect again</button>}</div>
            </>}
          </div>
        </div>
      )}
      {phase === 'connected' && portrait && !unstable && <div className="rs-hint"><Icon name="smartphone" size={14} />Turn your phone sideways for a bigger screen</div>}
      {phase === 'connected' && unstable && <div className="rs-unstable"><Icon name="wifi-off" size={14} />The connection is slow…</div>}

      {/* phones: the keys a phone keyboard lacks, and the touch bar */}
      {mobile && phase === 'connected' && keys && (
        <div className="rs-keys" role="toolbar" aria-label="Special keys">
          {[['Esc', KEY.esc], ['Tab', KEY.tab]].map(([l, k]) => <button key={l} type="button" onClick={() => tap(k as number)}>{l}</button>)}
          {[['Ctrl', KEY.ctrl], ['Alt', KEY.alt], [m?.kind === 'mac' ? '⌘' : 'Win', cmd], ['Shift', KEY.shift]].map(([l, k]) => <button key={l} type="button" className={latched.includes(k as number) ? 'on' : ''} onClick={() => latch(k as number)}>{l}</button>)}
          {[['←', KEY.left], ['↑', KEY.up], ['↓', KEY.down], ['→', KEY.right]].map(([l, k]) => <button key={l} type="button" onClick={() => tap(k as number)}>{l}</button>)}
          {[['Del', KEY.del], ['Home', KEY.home], ['End', KEY.end], ['PgUp', KEY.pgup], ['PgDn', KEY.pgdn]].map(([l, k]) => <button key={l} type="button" onClick={() => tap(k as number)}>{l}</button>)}
          {Array.from({ length: 12 }, (_, i) => <button key={`f${i}`} type="button" onClick={() => tap(KEY.f(i + 1))}>F{i + 1}</button>)}
        </div>
      )}
      {mobile && (
        <nav className="rs-bar" aria-label="Session">
          <button type="button" onClick={askEnd}><Icon name="x" size={20} /><span>End</span></button>
          <button type="button" onClick={showKeyboard} disabled={phase !== 'connected'}><Icon name="keyboard" size={20} /><span>Keyboard</span></button>
          <button type="button" className={keys ? 'on' : ''} onClick={() => setKeys(k => !k)} disabled={phase !== 'connected'}><Icon name="command" size={20} /><span>Keys</span></button>
          <button type="button" onClick={() => setMode(mode === 'trackpad' ? 'touch' : 'trackpad')} disabled={phase !== 'connected'}><Icon name={mode === 'trackpad' ? 'pointer' : 'hand'} size={20} /><span>{mode === 'trackpad' ? 'Trackpad' : 'Touch'}</span></button>
          <button type="button" onClick={() => zoomStep(1)} disabled={phase !== 'connected'} onContextMenu={e => { e.preventDefault(); setZoom('fit') }}><Icon name="zoom-in" size={20} /><span>{zoom === 'fit' ? 'Zoom' : `${Math.round((zoom as number) * 100)}%`}</span></button>
          {zoom !== 'fit' && <button type="button" onClick={() => zoomStep(-1)}><Icon name="zoom-out" size={20} /><span>Less</span></button>}
          <button type="button" onClick={e => setMenu(rectOf(e.currentTarget))}><Icon name="more" size={20} /><span>More</span></button>
        </nav>
      )}

      {menu && <Menu anchor={menu} align="right" items={moreItems()} onClose={() => setMenu(null)} width={240} />}
      {sheet === 'clip' && <ClipSheet remote={remoteClip} onClose={() => setSheet(null)} onSend={sendClip} onType={t => { if (c) { typeText(c, t); toast('Typed on the computer') } }} />}
      {sheet === 'files' && m && <Modal title={`Files on ${m.name}`} onClose={() => setSheet(null)} size="lg"><FileBrowser machine={m} compact /></Modal>}
      {leave && <Confirm title={`End the session to ${m?.name}?`} message="The computer stays as it is; only this window closes." confirmLabel="End" danger={false} onCancel={() => setLeave(false)} onConfirm={end} />}
    </div>
  )
}

function ClipSheet({ remote, onClose, onSend, onType }: { remote: string; onClose: () => void; onSend: (t: string) => void; onType: (t: string) => void }) {
  const [text, setText] = useState('')
  const { toast } = useToast()
  return (
    <Modal title="Clipboard and typing" onClose={onClose}>
      <div className="stack">
        <div>
          <h4 className="mf-h">From the computer</h4>
          {remote ? <><pre className="clip-pre">{remote}</pre><button type="button" className="btn sm" onClick={async () => toast(await copyText(remote) ? 'Copied here' : 'Could not copy', 'ok')}><Icon name="copy" size={13} />Copy here</button></>
            : <p className="muted small">Copy something on the computer and it appears here.</p>}
        </div>
        <div>
          <h4 className="mf-h">To the computer</h4>
          <textarea value={text} onChange={e => setText(e.target.value)} rows={4} placeholder="Paste or type here…" autoFocus />
          <div className="row wrap mt">
            <button type="button" className="btn primary sm" disabled={!text} onClick={() => { onSend(text); onClose() }}><Icon name="clipboard" size={13} />Put on its clipboard</button>
            <button type="button" className="btn sm" disabled={!text} onClick={() => { onType(text); onClose() }} title="For a sign-in screen, where paste does not work"><Icon name="keyboard" size={13} />Type it out</button>
          </div>
          <p className="muted small mt">“Type it out” presses the keys one by one: it works on a sign-in screen, where paste does not.</p>
        </div>
      </div>
    </Modal>
  )
}

