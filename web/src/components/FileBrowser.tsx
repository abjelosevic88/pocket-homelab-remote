// A computer's files over SFTP: browse, download here, upload from here, save to Pocket Files.
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { fmtBytes, fmtDateTime } from '../lib/format'
import type { FileEntry, Listing, Machine } from '../lib/types'
import { Icon } from './icons'
import { Loading, MenuButton, Switch, useToast } from './ui'

const enc = encodeURIComponent

export function FileBrowser({ machine, compact = false }: { machine: Machine; compact?: boolean }) {
  const { settings } = useApp()
  const { toast } = useToast()
  const key = `prm-files-${machine.id}`
  const [path, setPath] = useState(() => sessionStorage.getItem(key) || '')
  const [list, setList] = useState<Listing | null>(null)
  const [err, setErr] = useState<{ msg: string; hostKey?: boolean } | null>(null)
  const [loading, setLoading] = useState(false)
  const [hidden, setHidden] = useState(false)
  const [up, setUp] = useState<{ name: string; pct: number } | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const filesOK = !!settings?.files_via
  const coreOn = !!settings?.core_configured && settings?.core_registered !== false

  const load = useCallback(async (p: string) => {
    setLoading(true)
    try {
      const l = await api.get<Listing>(`${V1}/machines/${machine.id}/files?path=${enc(p)}`)
      setList(l); setErr(null); setPath(l.path); sessionStorage.setItem(key, l.path)
    } catch (e) {
      const d = (e as ApiError).data || {}
      setErr({ msg: (e as Error).message, hostKey: !!d.host_key_changed })
    } finally { setLoading(false) }
  }, [machine.id, key])
  useEffect(() => { load(path) }, [machine.id]) // eslint-disable-line react-hooks/exhaustive-deps

  const trust = async () => { await api.post(`${V1}/machines/${machine.id}/trust-again`); load(path) }
  const save = async (f: FileEntry) => {
    try {
      const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/machines/${machine.id}/files/save`, { path: f.path })
      toast(r.ok ? r.message! : r.error!, r.ok ? 'ok' : 'err')
    } catch (e) { toast((e as Error).message, 'err') }
  }
  const upload = (files: FileList | null) => {
    if (!files || !files.length || !list) return
    const queue = Array.from(files)
    const next = () => {
      const file = queue.shift()
      if (!file) { setUp(null); load(list.path); return }
      const fd = new FormData()
      fd.append('file', file)
      const xhr = new XMLHttpRequest()
      xhr.open('POST', `${V1}/machines/${machine.id}/files/upload?path=${enc(list.path)}`)
      xhr.upload.onprogress = e => { if (e.lengthComputable) setUp({ name: file.name, pct: Math.round(e.loaded / e.total * 100) }) }
      xhr.onload = () => {
        if (xhr.status >= 300) { let m = xhr.statusText; try { m = JSON.parse(xhr.responseText).error || m } catch { /* plain text */ } toast(`${file.name}: ${m}`, 'err') } else toast(`${file.name} sent`)
        next()
      }
      xhr.onerror = () => { toast(`${file.name}: the upload broke off`, 'err'); next() }
      setUp({ name: file.name, pct: 0 })
      xhr.send(fd)
    }
    next()
  }

  const crumbs = list ? list.path.split('/').filter(Boolean) : []
  const entries = (list?.entries || []).filter(e => hidden || !e.hidden)
  return (
    <div className={`fb ${compact ? 'compact' : ''}`}>
      <div className="fb-bar">
        <button type="button" className="btn ghost icon-only sm" title="Up a folder" aria-label="Up a folder" disabled={!list?.parent || loading} onClick={() => list && load(list.parent)}><Icon name="arrow-up" size={15} /></button>
        <button type="button" className="btn ghost icon-only sm" title="Home folder" aria-label="Home folder" disabled={loading} onClick={() => load(list?.home || '')}><Icon name="home" size={15} /></button>
        <div className="fb-crumbs grow">
          <button type="button" className="fb-crumb" onClick={() => load('/')}>/</button>
          {crumbs.map((c, i) => <span key={i} className="fb-crumb-wrap"><button type="button" className="fb-crumb" onClick={() => load('/' + crumbs.slice(0, i + 1).join('/'))}>{c}</button>{i < crumbs.length - 1 && <span className="muted">/</span>}</span>)}
        </div>
        <button type="button" className="btn ghost icon-only sm" title="Reload" aria-label="Reload" disabled={loading} onClick={() => load(path)}><Icon name="refresh" size={15} /></button>
        <button type="button" className="btn sm" disabled={!list || !!up} onClick={() => input.current?.click()}><Icon name="upload" size={14} />{compact ? 'Send' : 'Send a file here'}</button>
        <input ref={input} type="file" multiple hidden onChange={e => { upload(e.target.files); e.target.value = '' }} />
      </div>
      {up && <div className="fb-progress"><span className="ellipsis grow">Sending {up.name}</span><span className="tnum">{up.pct}%</span><div className="fb-pbar"><span style={{ width: `${up.pct}%` }} /></div></div>}
      {err && <div className={`callout ${err.hostKey ? 'danger' : 'warn'} mb`}><Icon name="alert" size={15} /><div>{err.msg}{err.hostKey && <div className="mt"><button type="button" className="btn sm" onClick={trust}>Trust it again</button></div>}</div></div>}
      {!list && !err && <Loading />}
      {list && <div className="fb-list" aria-busy={loading}>
        {entries.length === 0 && <p className="muted fb-empty">This folder is empty.</p>}
        {entries.map(f => (
          <div key={f.path} className={`fb-row ${f.dir ? 'dir' : ''}`}>
            {f.dir
              ? <button type="button" className="fb-name" onClick={() => load(f.path)}><Icon name="folder" size={16} className="fb-ico" /><span className="ellipsis">{f.name}</span></button>
              : <a className="fb-name" href={`${V1}/machines/${machine.id}/files/download?path=${enc(f.path)}`}><Icon name="file" size={16} className="fb-ico" /><span className="ellipsis">{f.name}</span></a>}
            {!compact && <span className="fb-when muted small">{fmtDateTime(f.modified)}</span>}
            <span className="fb-size muted small tnum">{f.dir ? '' : fmtBytes(f.size)}</span>
            {!f.dir ? <MenuButton label={`More for ${f.name}`} align="right" width={240} items={[
              { label: 'Download here', icon: 'download', onSelect: () => { window.location.href = `${V1}/machines/${machine.id}/files/download?path=${enc(f.path)}` } },
              { label: 'Open in the browser', icon: 'external', onSelect: () => window.open(`${V1}/machines/${machine.id}/files/download?inline=1&path=${enc(f.path)}`, '_blank') },
              { label: filesOK ? 'Save to Pocket Files' : 'Save to Pocket Files (set up first)', icon: 'archive', disabled: !filesOK, onSelect: () => save(f) },
            ]}><Icon name="more" size={15} /></MenuButton> : <span className="fb-gap" />}
          </div>
        ))}
      </div>}
      <div className="fb-foot row wrap">
        <Switch size="sm" checked={hidden} onChange={setHidden} label="Show hidden files" /><span className="small muted">Show hidden files</span>
        <span className="grow" />
        {!filesOK && <span className="small muted" title={coreOn ? '' : 'Needs Pocket Core'}>Saving to Pocket Files: {coreOn ? 'switch it on in Settings → Connected apps' : 'needs Pocket Core'}</span>}
        {filesOK && <span className="small muted">Pocket Files folder: {settings?.files_folder}/{machine.name}</span>}
      </div>
    </div>
  )
}
