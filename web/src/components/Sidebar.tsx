// Left column: add button, sections, the computers online now — the Pocket Calendar sidebar.
import { useState } from 'react'
import { Link, NavLink, useNavigate } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { kindIcon } from '../lib/remote'
import type { Machine } from '../lib/types'
import { Icon } from './icons'
import { createItems, type CreateKind } from './Layout'
import { type Anchor, Menu, rectOf } from './ui'

export function Sidebar({ onCreate, onNavigate, machines, tailnet }: { onCreate: (k: CreateKind) => void; onNavigate: () => void; machines: Machine[] | null; tailnet: boolean }) {
  const { status, settings } = useApp()
  const navigate = useNavigate()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const title = settings?.title || status?.title || 'Pocket Remote'
  const online = (machines || []).filter(m => m.status.state === 'online')
  const live = (machines || []).reduce((n, m) => n + m.live, 0)
  const nav: { to: string; label: string; icon: string; end?: boolean; count?: string; badge?: string }[] = [
    { to: '/', label: 'Computers', icon: 'monitor', end: true, count: machines ? `${online.length}/${machines.length}` : undefined },
    { to: '/sessions', label: 'Sessions', icon: 'clock', badge: live ? `${live} open` : undefined },
  ]
  const go = (url: string) => { onNavigate(); navigate(url) }
  return (
    <aside className="sidebar" aria-label="Remote">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Remote' ? title : <>Pocket <b>Remote</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => tailnet ? setMenu(menu ? null : rectOf(e.currentTarget)) : onCreate('machine')}>
        <Icon name="plus" size={15} strokeWidth={2.2} />Add a computer{tailnet && <Icon name="chevron-down" size={15} className="create-caret" />}
      </button>
      {menu && <Menu anchor={menu} items={createItems(onCreate, tailnet)} onClose={() => setMenu(null)} width={248} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}
            {n.badge ? <span className="sb-badge">{n.badge}</span> : !!n.count && <span className="sb-count">{n.count}</span>}
          </NavLink>
        ))}
      </nav>

      <section className="sb-sec">
        <h4 className="sb-h"><span className="sb-h-btn">Online now</span></h4>
        <div className="sb-machines">
          {machines && online.length === 0 && <p className="muted small sb-none">None of your computers answers right now.</p>}
          {online.map(m => (
            <button key={m.id} type="button" className="sb-machine" onClick={() => go(`/connect/${m.id}`)} title={`Connect to ${m.name}`}>
              <span className="dot-online" /><Icon name={kindIcon(m.kind)} size={14} /><span className="ellipsis grow">{m.name}</span><Icon name="log-in" size={13} className="muted" />
            </button>
          ))}
        </div>
      </section>

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('prm:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}
