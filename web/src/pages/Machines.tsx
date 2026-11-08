// Home: your computers with who is online, connect / wake / files, and what is open right now.
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, plural } from '../lib/format'
import { KIND_NAME, PROTOCOL_NAME, fmtSeconds, kindIcon, statusLine } from '../lib/remote'
import type { Machine, Overview } from '../lib/types'
import { Icon } from '../components/icons'
import { openCreate, openEdit, useMachines, useOnChange } from '../components/Layout'
import { Confirm, Empty, Loading, MenuButton, useAsync, useToast } from '../components/ui'

export default function Machines() {
  const { machines, reload } = useMachines()
  const { settings, user } = useApp()
  const { data: ov, reload: reloadOv } = useAsync(() => api.get<Overview>(`${V1}/overview`), [])
  useOnChange(reloadOv)
  const { toast } = useToast()
  const [del, setDel] = useState<Machine | null>(null)
  const tailnet = !!settings?.tailnet && user?.role === 'admin'
  if (!machines) return <Loading />
  const online = machines.filter(m => m.status.state === 'online').length
  const endLive = async (id: number) => { try { await api.post(`${V1}/sessions/${id}/end`); toast('Session ended'); setTimeout(() => { reloadOv(); reload() }, 600) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h2>Your computers</h2>
          <p className="muted">{machines.length ? `${online} of ${plural(machines.length, 'computer')} online` : 'Reach your own computers from this phone or browser.'}{ov && ov.week.sessions > 0 ? ` · ${plural(ov.week.sessions, 'session')} this week, ${fmtSeconds(ov.week.seconds)}` : ''}</p>
        </div>
        <div className="row">
          {tailnet && <button type="button" className="btn" onClick={() => openCreate('tailnet')}><Icon name="wifi" size={15} />From your tailnet</button>}
          <button type="button" className="btn primary" onClick={() => openCreate('machine')}><Icon name="plus" size={15} />Add a computer</button>
        </div>
      </div>

      {ov && !ov.guacd.ok && <div className="callout warn mb"><Icon name="alert" size={15} /><div><b>The remote desktop engine is not running.</b> Computers can be added and woken, but not opened until it is: {ov.guacd.detail}.</div></div>}

      {ov && ov.live.length > 0 && <div className="live-box mb">
        {ov.live.map(l => (
          <div key={l.id} className="live-row">
            <span className="dot-live" />
            <span className="grow"><b>{l.machine}</b> is open on {l.device} · since {ago(l.started_at)}</span>
            <button type="button" className="btn sm ghost danger-text" onClick={() => endLive(l.id)}>End it</button>
          </div>
        ))}
      </div>}

      {machines.length === 0 ? <Welcome tailnet={tailnet} /> : (
        <div className="machines">
          {machines.map(m => <MachineCard key={m.id} m={m} onChanged={reload} onDelete={() => setDel(m)} />)}
        </div>
      )}
      {del && <Confirm confirmLabel="Remove" title={`Remove ${del.name}?`} message="Its sign-in is deleted. Past sessions stay in the log." onCancel={() => setDel(null)}
        onConfirm={async () => { const d = del; setDel(null); try { await api.del(`${V1}/machines/${d.id}`); toast(`${d.name} removed`); reload() } catch (e) { toast((e as Error).message, 'err') } }} />}
    </div>
  )
}

function MachineCard({ m, onChanged, onDelete }: { m: Machine; onChanged: () => void; onDelete: () => void }) {
  const navigate = useNavigate()
  const { toast } = useToast()
  const [busy, setBusy] = useState('')
  const state = m.status.waking_since && m.status.state !== 'online' ? 'waking' : m.status.state
  const wake = async () => {
    setBusy('wake')
    try { const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/machines/${m.id}/wake`); toast(r.ok ? r.message! : r.error!, r.ok ? 'ok' : 'err'); onChanged() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy('') }
  }
  const check = async () => { setBusy('check'); try { await api.post(`${V1}/machines/${m.id}/check`); onChanged() } finally { setBusy('') } }
  return (
    <article className={`mc ${state}`}>
      <div className="mc-top">
        <span className={`mc-ico k-${m.kind}`}><Icon name={kindIcon(m.kind)} size={22} /></span>
        <div className="grow mc-title">
          <h3 className="ellipsis">{m.name}{m.favorite && <Icon name="star" size={13} className="mc-fav" />}</h3>
          <div className="muted small ellipsis">{KIND_NAME[m.kind]} · {PROTOCOL_NAME[m.protocol]} · {m.host}</div>
        </div>
        <MenuButton label={`More for ${m.name}`} align="right" items={[
          { label: 'Edit', icon: 'edit', onSelect: () => openEdit(m) },
          { label: 'Check now', icon: 'refresh', onSelect: check },
          ...(m.files_enabled ? [{ label: 'Files', icon: 'folder', onSelect: () => navigate(`/machines/${m.id}/files`) }] : []),
          { label: 'Its sessions', icon: 'clock', onSelect: () => navigate(`/sessions?machine=${m.id}`) },
          'sep',
          { label: 'Remove', icon: 'trash', danger: true, onSelect: onDelete },
        ]}><Icon name="more" size={16} /></MenuButton>
      </div>
      <div className="mc-status">
        <span className={`mc-dot ${state}`} />
        <span className="ellipsis">{statusLine(m)}</span>
        {m.tailnet && m.status.state !== 'online' && <span className="muted small mc-tn" title="What Tailscale says">{m.tailnet.online ? 'on the tailnet' : 'off the tailnet'}</span>}
      </div>
      <div className="mc-meta muted small">
        {m.live > 0 ? <span className="ok">Open now{m.live > 1 ? ` (${m.live})` : ''}</span> : m.last_used_at ? `Last used ${ago(m.last_used_at)}` : 'Not used yet'}
        {!m.has_password && !m.has_key && m.protocol !== 'vnc' && <span className="warn-text"> · no sign-in saved</span>}
      </div>
      <div className="mc-actions">
        <Link to={`/connect/${m.id}`} className="btn primary sm"><Icon name="log-in" size={14} />Connect</Link>
        {m.mac_address && state !== 'online' && <button type="button" className="btn sm" onClick={wake} disabled={busy === 'wake' || state === 'waking'}><Icon name="power" size={14} />{state === 'waking' ? 'Waking…' : 'Wake'}</button>}
        {m.files_enabled && <Link to={`/machines/${m.id}/files`} className="btn sm ghost"><Icon name="folder" size={14} />Files</Link>}
      </div>
    </article>
  )
}

function Welcome({ tailnet }: { tailnet: boolean }) {
  return (
    <div className="welcome">
      <Empty icon="monitor" title="No computers yet">
        Add your Mac, a Windows PC or a Linux machine. Pocket Remote reaches it over your tailnet, so nothing is opened to the internet.
      </Empty>
      <div className="row center wrap">
        {tailnet && <button type="button" className="btn" onClick={() => openCreate('tailnet')}><Icon name="wifi" size={15} />Pick from your tailnet</button>}
        <button type="button" className="btn primary" onClick={() => openCreate('machine')}><Icon name="plus" size={15} />Add a computer</button>
      </div>
      <div className="how-grid">
        <div className="how"><Icon name="laptop" size={18} /><b>Mac</b><span>System Settings → General → Sharing → <b>Screen Sharing</b> on. <b>Remote Login</b> too, for files.</span></div>
        <div className="how"><Icon name="monitor" size={18} /><b>Windows PC</b><span>Settings → System → <b>Remote Desktop</b> on (Windows Pro). OpenSSH Server, for files.</span></div>
        <div className="how"><Icon name="server" size={18} /><b>Linux</b><span>An SSH server for a terminal and files; VNC or xrdp for a desktop.</span></div>
      </div>
    </div>
  )
}
