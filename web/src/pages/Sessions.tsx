// /sessions — every remote session (who, which computer, from where, how long) and the files moved.
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { ago, fmtBytes, fmtDate, fmtISOTime } from '../lib/format'
import { PROTOCOL_NAME, fmtSeconds } from '../lib/remote'
import type { LiveSession, Overview, RemoteSession, Transfer } from '../lib/types'
import { Icon } from '../components/icons'
import { useMachines } from '../components/Layout'
import { Select } from '../components/Select'
import { Empty, Loading, Tabs, useAsync, useToast } from '../components/ui'

type Tab = 'sessions' | 'files'

export default function Sessions() {
  const [sp, setSp] = useSearchParams()
  const { machines } = useMachines()
  const { toast } = useToast()
  const machine = sp.get('machine') || ''
  const tab: Tab = sp.get('tab') === 'files' ? 'files' : 'sessions'
  const [limit, setLimit] = useState(60)
  const { data, reload } = useAsync(() => api.get<{ items: RemoteSession[]; total: number; live: LiveSession[] }>(`${V1}/sessions?limit=${limit}${machine ? `&machine=${machine}` : ''}`), [machine, limit])
  const { data: ov } = useAsync(() => api.get<Overview>(`${V1}/overview`), [])
  const { data: transfers } = useAsync(() => tab === 'files' ? api.get<Transfer[]>(`${V1}/transfers?limit=200`) : Promise.resolve(null), [tab])
  const set = (k: string, v: string) => { const n = new URLSearchParams(sp); if (v) n.set(k, v); else n.delete(k); setSp(n, { replace: true }) }
  const end = async (id: number) => { try { await api.post(`${V1}/sessions/${id}/end`); toast('Session ended'); setTimeout(reload, 600) } catch (e) { toast((e as Error).message, 'err') } }

  const days: [string, RemoteSession[]][] = []
  for (const s of data?.items || []) {
    const d = new Date(s.started_at).toLocaleDateString('sv-SE')
    const last = days[days.length - 1]
    if (last && last[0] === d) last[1].push(s); else days.push([d, [s]])
  }
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow"><h2>Sessions</h2><p className="muted">Every time one of your computers was opened from here: when, from which device and for how long. Not you? End it, then change that computer's password.</p></div>
      </div>
      {ov && <div className="stat-row mb">
        <div className="stat"><span className="stat-n tnum">{ov.week.sessions}</span><span className="stat-l">sessions this week</span></div>
        <div className="stat"><span className="stat-n tnum">{fmtSeconds(ov.week.seconds)}</span><span className="stat-l">connected this week</span></div>
        <div className="stat"><span className="stat-n tnum">{ov.month.sessions}</span><span className="stat-l">in 30 days</span></div>
        <div className="stat"><span className="stat-n tnum">{ov.month.machines}</span><span className="stat-l">computers used</span></div>
      </div>}
      {data && data.live.length > 0 && <div className="live-box mb">
        {data.live.map(l => (
          <div key={l.id} className="live-row"><span className="dot-live" /><span className="grow"><b>{l.machine}</b> is open on {l.device} · since {ago(l.started_at)}</span><button type="button" className="btn sm ghost danger-text" onClick={() => end(l.id)}>End it</button></div>
        ))}
      </div>}
      <div className="row wrap mb sess-tools">
        <Tabs tabs={[{ id: 'sessions', label: 'Sessions', icon: 'clock' }, { id: 'files', label: 'Files moved', icon: 'folder' }]} value={tab} onChange={t => set('tab', t === 'sessions' ? '' : t)} />
        <span className="grow" />
        {tab === 'sessions' && <Select ariaLabel="Computer" value={machine} onChange={v => set('machine', v)} options={[{ value: '', label: 'All computers' }, ...(machines || []).map(m => ({ value: String(m.id), label: m.name }))]} />}
      </div>
      {tab === 'sessions' && (!data ? <Loading /> : data.items.length === 0 ? <Empty icon="clock" title="No sessions yet">Connect to one of your computers and it shows up here.</Empty> : <>
        {days.map(([d, list]) => (
          <section key={d} className="sess-day">
            <h4 className="sess-h">{fmtDate(d)}</h4>
            <div className="sess-list">
              {list.map(s => (
                <div key={s.id} className="sess-row">
                  <span className="sess-time tnum">{fmtISOTime(s.started_at)}</span>
                  <div className="grow sess-main">
                    <div className="ellipsis">{s.machine_id ? <Link to={`/sessions?machine=${s.machine_id}`}>{s.machine_name}</Link> : s.machine_name} <span className="muted small">· {PROTOCOL_NAME[s.protocol] || s.protocol}</span></div>
                    <div className="muted small ellipsis">{s.device || 'Unknown device'}{s.ip ? ` · ${s.ip}` : ''}{s.bytes_out ? ` · ${fmtBytes(s.bytes_out)} of screen` : ''}</div>
                  </div>
                  <span className="sess-dur tnum">{s.ended_at ? fmtSeconds(s.duration_s) : <span className="ok">open</span>}</span>
                  {s.end_reason && s.end_reason !== 'closed' && <span className={`badge ${s.end_reason.startsWith('error') || s.end_reason === 'lost' ? 'warn' : ''}`} title={s.end_reason}>{s.end_reason === 'lost' ? 'connection lost' : s.end_reason.replace(/^error: /, 'error')}</span>}
                </div>
              ))}
            </div>
          </section>
        ))}
        {data.items.length < data.total && <div className="row center mt"><button type="button" className="btn" onClick={() => setLimit(l => l + 100)}>Show more ({data.total - data.items.length})</button></div>}
      </>)}
      {tab === 'files' && (!transfers ? <Loading /> : transfers.length === 0 ? <Empty icon="folder" title="No files moved yet">Downloads, uploads and saves to Pocket Files show up here.</Empty> :
        <div className="sess-list">
          {transfers.map(t => (
            <div key={t.id} className="sess-row">
              <Icon name={t.direction === 'upload' ? 'upload' : t.direction === 'to_files' ? 'archive' : 'download'} size={16} className="muted" />
              <div className="grow sess-main">
                <div className="ellipsis">{t.name}</div>
                <div className="muted small ellipsis">{t.direction === 'upload' ? `Sent to ${t.machine_name}` : t.direction === 'to_files' ? `${t.machine_name} → Pocket Files${t.target ? ` · ${t.target}` : ''}` : `From ${t.machine_name}`} · {ago(t.created_at)}</div>
              </div>
              <span className="sess-dur tnum muted small">{fmtBytes(t.size)}</span>
              {t.status === 'failed' && <span className="badge warn" title={t.error}>failed</span>}
            </div>
          ))}
        </div>)}
    </div>
  )
}
