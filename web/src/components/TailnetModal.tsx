// "Add from your tailnet": the devices Tailscale knows, ready to add as computers.
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { ago } from '../lib/format'
import { KIND_NAME, kindIcon } from '../lib/remote'
import type { TailnetListDevice } from '../lib/types'
import { Icon } from './icons'
import type { MachineDraft } from './MachineForm'
import { Loading, Modal, Switch, useAsync } from './ui'

const nice = (name: string) => name.replace(/[-_]+/g, ' ').replace(/(^|\s)\p{L}/gu, c => c.toUpperCase())

export function TailnetModal({ onClose, onPick }: { onClose: () => void; onPick: (d: MachineDraft) => void }) {
  const { data, error } = useAsync(() => api.get<{ available: boolean; detail?: string; devices: TailnetListDevice[] }>(`${V1}/tailnet`), [])
  const [all, setAll] = useState(false)
  const shown = (data?.devices || []).filter(d => all || d.fit)
  const hidden = (data?.devices || []).length - shown.length
  return (
    <Modal title="Add from your tailnet" onClose={onClose}>
      {error && <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>}
      {!data && !error && <Loading />}
      {data && !data.available && <div className="callout info"><Icon name="info" size={15} /><span>{data.detail}</span></div>}
      {data?.available && <>
        <p className="muted small mb">The computers on this box's tailnet. Choose one to fill in the form; you add the sign-in next.</p>
        <div className="tn-list">
          {shown.map(d => (
            <div key={d.id} className="tn-row">
              <span className={`tn-ico ${d.online ? 'on' : ''}`}><Icon name={kindIcon(d.kind)} size={18} /></span>
              <div className="grow">
                <div className="tn-name">{d.name}</div>
                <div className="muted small ellipsis">{d.os || KIND_NAME[d.kind]} · {d.ips[0]} · {d.online ? <span className="ok">online</span> : d.last_seen ? `seen ${ago(d.last_seen)}` : 'offline'}</div>
              </div>
              {d.added_machine_id ? <span className="badge">added</span>
                : <button type="button" className="btn sm" onClick={() => onPick({ name: nice(d.name), kind: d.kind, host: d.dns_name || d.ips[0], protocol: d.protocol, tailnet_id: d.id })}><Icon name="plus" size={13} />Add</button>}
            </div>
          ))}
          {shown.length === 0 && <p className="muted">No computers on the tailnet.</p>}
        </div>
        {(hidden > 0 || all) && <div className="row mt"><Switch checked={all} onChange={setAll} size="sm" label="Show phones and TVs" /><span className="small muted">Show phones, tablets and TVs too{hidden > 0 ? ` (${hidden})` : ''}</span></div>}
      </>}
    </Modal>
  )
}
