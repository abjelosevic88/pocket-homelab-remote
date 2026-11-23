// "A computer anywhere": pair Pocket Wave, which connects out through Pocket Core's relay.
import { useEffect, useRef, useState } from 'react'
import { api, V1 } from '../lib/api'
import type { Machine } from '../lib/types'
import { Icon } from './icons'
import { Modal, useToast } from './ui'

interface PairLink { url: string; address: string; expires_at: string }

export function AgentModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { toast } = useToast()
  const [link, setLink] = useState<PairLink | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [arrived, setArrived] = useState<Machine | null>(null)
  const known = useRef<Set<number> | null>(null)

  const make = async () => {
    setBusy(true); setErr('')
    try { setLink(await api.post<PairLink>(`${V1}/agents/pair-link`, {})) } catch (e) { setErr((e as Error).message) } finally { setBusy(false) }
  }
  useEffect(() => { make() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // The computer shows up by itself once the agent is paired.
  useEffect(() => {
    let stop = false
    const tick = async () => {
      const ms = await api.get<Machine[]>(`${V1}/machines`).catch(() => null)
      if (!ms || stop) return
      const agents = ms.filter(m => m.agent_id)
      if (!known.current) { known.current = new Set(agents.map(m => m.id)); return }
      const fresh = agents.find(m => !known.current!.has(m.id) && m.agent?.connected)
      if (fresh) { setArrived(fresh); onDone() }
    }
    tick()
    const t = setInterval(() => { if (!arrived) tick() }, 2500)
    return () => { stop = true; clearInterval(t) }
  }, [arrived]) // eslint-disable-line react-hooks/exhaustive-deps

  const copy = async () => {
    if (!link) return
    try { await navigator.clipboard.writeText(link.url); toast('Link copied') } catch { toast('Select the link and copy it', 'err') }
  }
  const mins = link ? Math.max(1, Math.round((new Date(link.expires_at).getTime() - Date.now()) / 60000)) : 0

  return (
    <Modal title="A computer anywhere" onClose={onClose}
      footer={<div className="form-actions"><button type="button" className="btn" onClick={onClose}>{arrived ? 'Done' : 'Close'}</button></div>}>
      {arrived ? (
        <div className="agent-done">
          <Icon name="check" size={22} />
          <div><b>{arrived.name} is here.</b><div className="muted small">Open its terminal or files from the list.</div></div>
        </div>
      ) : <>
        <p className="muted small mb">For a Mac outside your tailnet. Pocket Wave connects out through Pocket Core's relay: no VPN, nothing opened on the Mac.</p>
        <ol className="agent-steps">
          <li><b>Install Pocket Wave</b> on the Mac <span className="muted">(Pocket Files → Pocket Apps)</span>.</li>
          <li><b>Copy this link</b> and paste it into Pocket Wave's <b>Pair</b> window.
            {err && <div className="callout danger mt-sm"><Icon name="alert" size={15} /><span>{err}</span></div>}
            {link && <div className="agent-link">
              <input readOnly value={link.url} className="mono" onFocus={e => e.currentTarget.select()} aria-label="Pairing link" />
              <button type="button" className="btn primary sm" onClick={copy}><Icon name="copy" size={14} />Copy</button>
            </div>}
            {link && <div className="muted small">Works once, for {mins} min · <button type="button" className="link-btn" onClick={make} disabled={busy}>new link</button></div>}
            {!link && !err && <div className="muted small">Making a link…</div>}
          </li>
          <li><b>That's it.</b> The Mac shows up here by itself, with its terminal and files.</li>
        </ol>
        <p className="mf-how muted small"><Icon name="info" size={13} /><span>The desktop works too if Screen Sharing is on in the Mac's Sharing settings.</span></p>
      </>}
    </Modal>
  )
}
