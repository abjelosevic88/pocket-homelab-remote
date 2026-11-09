// Add or edit a computer: what it is, how to reach it, how to sign in, waking it, files and display.
import React, { useState } from 'react'
import { api, V1 } from '../lib/api'
import { DEFAULT_PORT, DEFAULT_PROTOCOL, KIND_NAME, PROTOCOL_NAME, PROTOCOL_TECH } from '../lib/remote'
import type { Kind, Machine, MachineOptions, Protocol } from '../lib/types'
import { Icon } from './icons'
import { Select } from './Select'
import { Field, Modal, Segmented, Switch, useToast } from './ui'

/** A new machine's starting values (from the tailnet list). */
export type MachineDraft = Partial<Pick<Machine, 'name' | 'kind' | 'host' | 'protocol' | 'tailnet_id'>>

const HOW: Record<Kind, React.ReactNode> = {
  mac: <>On the Mac: <b>System Settings → General → Sharing</b>, switch on <b>Screen Sharing</b>. For files, switch on <b>Remote Login</b> too. Sign in here with your Mac account name and password.</>,
  windows: <>On the PC: <b>Settings → System → Remote Desktop</b>, switch it on (Windows Pro). Sign in with the PC's account; for a Microsoft account use its email. For files, add <b>OpenSSH Server</b> under Optional features.</>,
  linux: <>A terminal over SSH works on any Linux machine with an SSH server. A desktop needs a desktop installed plus a VNC server (Screen Sharing) or xrdp (Remote Desktop) running on it.</>,
  other: <>Anything that speaks VNC, Remote Desktop or SSH: a Raspberry Pi, a NAS, a virtual machine.</>,
}

export function MachineModal({ machine, draft, onClose, onSaved }: { machine?: Machine; draft?: MachineDraft; onClose: () => void; onSaved: (m: Machine) => void }) {
  const { toast } = useToast()
  const start = machine || draft || {}
  const kind0: Kind = (start.kind as Kind) || 'mac'
  const [f, setF] = useState({
    name: start.name || '', kind: kind0, host: start.host || '',
    protocol: (start.protocol && start.protocol !== 'ssh' ? start.protocol : DEFAULT_PROTOCOL[kind0]) as Protocol,
    desktop: machine ? machine.desktop_enabled : kind0 !== 'linux', terminal: machine ? machine.terminal_enabled : kind0 === 'mac' || kind0 === 'linux',
    port: machine?.port ? String(machine.port) : '', username: machine?.username || '', password: '', private_key: '', domain: machine?.domain || '',
    mac_address: machine?.mac_address || '', wol_broadcast: machine?.wol_broadcast || '', files_enabled: machine ? machine.files_enabled : kind0 !== 'windows',
    ssh_port: String(machine?.ssh_port || 22), notes: machine?.notes || '', favorite: machine?.favorite || false, tailnet_id: start.tailnet_id || '',
  })
  const [opt, setOpt] = useState<MachineOptions>(machine?.options || (kind0 === 'windows' ? { ignore_cert: true, resize: true } : {}))
  const [clearPw, setClearPw] = useState(false)
  const [clearKey, setClearKey] = useState(false)
  const [more, setMore] = useState(!!machine && (!!machine.mac_address || machine.files_enabled))
  const [display, setDisplay] = useState(false)
  const [busy, setBusy] = useState(false)
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF(x => ({ ...x, [k]: v }))
  const setKind = (k: Kind) => setF(x => machine ? { ...x, kind: k } : ({ ...x, kind: k, protocol: DEFAULT_PROTOCOL[k], files_enabled: k !== 'windows', desktop: k !== 'linux', terminal: k === 'mac' || k === 'linux' }))
  const p = f.protocol

  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    const body: Record<string, unknown> = {
      name: f.name, kind: f.kind, host: f.host, protocol: f.protocol, port: Number(f.port) || 0, username: f.username, domain: f.domain,
      desktop_enabled: f.desktop, terminal_enabled: f.terminal,
      mac_address: f.mac_address, wol_broadcast: f.wol_broadcast, files_enabled: f.files_enabled, ssh_port: Number(f.ssh_port) || 22,
      notes: f.notes, favorite: f.favorite, options: opt, tailnet_id: f.tailnet_id,
    }
    if (f.password) body.password = f.password
    if (f.private_key) body.private_key = f.private_key
    if (clearPw) body.clear_password = true
    if (clearKey) body.clear_key = true
    try {
      const m = machine ? await api.put<Machine>(`${V1}/machines/${machine.id}`, body) : await api.post<Machine>(`${V1}/machines`, body)
      toast(machine ? 'Saved' : `${m.name} added`)
      onSaved(m)
    } catch (err) { toast((err as Error).message, 'err') } finally { setBusy(false) }
  }

  return (
    <Modal title={machine ? `Edit ${machine.name}` : 'Add a computer'} onClose={onClose} size="lg"
      footer={<div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" form="machine-form" disabled={busy || !f.name || !f.host || (!f.desktop && !f.terminal)}>{machine ? 'Save' : 'Add computer'}</button></div>}>
      <form id="machine-form" onSubmit={save} className="mf">
        <Field group label="What is it">
          <Segmented value={f.kind} onChange={setKind} options={(['mac', 'windows', 'linux', 'other'] as Kind[]).map(k => ({ value: k, label: KIND_NAME[k] }))} />
        </Field>
        <p className="mf-how muted small"><Icon name="info" size={13} /><span>{HOW[f.kind]}</span></p>
        <div className="two-col tight">
          <Field label="Name"><input value={f.name} onChange={e => set('name', e.target.value)} placeholder={f.kind === 'mac' ? 'MacBook Pro' : f.kind === 'windows' ? 'Gaming PC' : 'Home server'} required maxLength={80} autoFocus={!machine} /></Field>
          <Field label="Address" help="Its tailnet name (macbook-pro) or an IP address."><input value={f.host} onChange={e => set('host', e.target.value)} placeholder="macbook-pro" required autoCapitalize="off" autoCorrect="off" spellCheck={false} /></Field>
        </div>
        <h4 className="mf-h"><Icon name="log-in" size={14} />Ways in</h4>
        <div className="mf-ways">
          <div className={`mf-way ${f.desktop ? 'on' : ''}`}>
            <Toggle checked={f.desktop} onChange={v => set('desktop', v)} label="Desktop" />
            <span className="muted small grow">Its screen, mouse and keyboard.</span>
            {f.desktop && <Segmented size="sm" value={p} onChange={v => set('protocol', v)} options={(['vnc', 'rdp'] as Protocol[]).map(x => ({ value: x, label: PROTOCOL_NAME[x], title: PROTOCOL_TECH[x] }))} />}
          </div>
          <div className={`mf-way ${f.terminal ? 'on' : ''}`}>
            <Toggle checked={f.terminal} onChange={v => set('terminal', v)} label="Terminal" />
            <span className="muted small grow">A shell over SSH, with the same sign-in.</span>
            {f.terminal && <label className="mf-port small">SSH port <input type="number" min={1} max={65535} value={f.ssh_port} onChange={e => set('ssh_port', e.target.value)} /></label>}
          </div>
          {!f.desktop && !f.terminal && <p className="mf-warn small"><Icon name="alert" size={13} /><span>Switch on at least one way in.</span></p>}
        </div>
        {f.desktop && f.kind === 'mac' && p === 'rdp' && <p className="mf-warn small"><Icon name="alert" size={13} /><span>A Mac has no Remote Desktop. Choose <b>Screen Sharing</b>.</span></p>}
        {f.desktop && f.kind === 'windows' && p === 'vnc' && <p className="mf-warn small"><Icon name="alert" size={13} /><span>Windows has no Screen Sharing (VNC) unless you installed a VNC server. Usually choose <b>Remote Desktop</b>.</span></p>}
        <h4 className="mf-h"><Icon name="key" size={14} />Sign-in</h4>
        <p className="muted small mf-note">Kept encrypted on the server; it never comes back to this browser.</p>
        <div className={f.desktop && p === 'rdp' ? 'three-col tight' : 'two-col tight'}>
          <Field label="User name" help={f.desktop && !f.terminal && p === 'vnc' && f.kind !== 'mac' ? 'Most VNC servers only need the password.' : undefined}><input value={f.username} onChange={e => set('username', e.target.value)} autoComplete="off" autoCapitalize="off" spellCheck={false} /></Field>
          <Field label="Password" help={machine?.has_password && !clearPw ? <>Saved. Type to replace · <button type="button" className="link-btn" onClick={() => setClearPw(true)}>remove it</button></> : clearPw ? 'Will be removed when you save.' : undefined}>
            <input type="password" value={f.password} onChange={e => { set('password', e.target.value); setClearPw(false) }} placeholder={machine?.has_password && !clearPw ? '••••••••' : ''} autoComplete="new-password" />
          </Field>
          {f.desktop && p === 'rdp' && <Field label="Domain" help="Usually empty."><input value={f.domain} onChange={e => set('domain', e.target.value)} autoComplete="off" /></Field>}
        </div>
        {(f.terminal || f.files_enabled) && <Field label="Private key (optional)" help={machine?.has_key && !clearKey ? <>A key is saved. Paste to replace · <button type="button" className="link-btn" onClick={() => setClearKey(true)}>remove it</button></> : 'An OpenSSH key instead of the password, for the terminal and files. If it has a passphrase, put it in Password.'}>
          <textarea className="mono mf-key" value={f.private_key} onChange={e => { set('private_key', e.target.value); setClearKey(false) }} placeholder={machine?.has_key && !clearKey ? '•••• a key is saved' : '-----BEGIN OPENSSH PRIVATE KEY-----'} rows={3} spellCheck={false} />
        </Field>}

        {!more && <button type="button" className="conn-advanced" onClick={() => setMore(true)}><Icon name="plus" size={13} />Wake it up, files, notes</button>}
        {more && <>
          <h4 className="mf-h"><Icon name="power" size={14} />Wake it up</h4>
          <div className="two-col tight">
            <Field label="Network card address" help="For Wake-on-LAN, on your home network only. Mac: System Settings → Network → Details → Hardware. Windows: getmac."><input value={f.mac_address} onChange={e => set('mac_address', e.target.value)} placeholder="a4:83:e7:12:9c:01" className="mono" autoCapitalize="off" spellCheck={false} /></Field>
            <Field label="Send to (optional)" help="Your network's broadcast address; empty = 255.255.255.255."><input value={f.wol_broadcast} onChange={e => set('wol_broadcast', e.target.value)} placeholder="192.168.1.255" className="mono" /></Field>
          </div>
          <h4 className="mf-h"><Icon name="folder" size={14} />Files</h4>
          <div className="mf-row">
            <Switch checked={f.files_enabled} onChange={v => set('files_enabled', v)} label="Browse its files" />
            <span className="grow small">Browse, download, upload and save to Pocket Files, over SFTP with the same sign-in.</span>
            {f.files_enabled && !f.terminal && <label className="mf-port small">SSH port <input type="number" min={1} max={65535} value={f.ssh_port} onChange={e => set('ssh_port', e.target.value)} /></label>}
          </div>
          <Field label="Notes"><textarea value={f.notes} onChange={e => set('notes', e.target.value)} rows={2} maxLength={4000} placeholder="Where it is, who uses it, what to know before connecting." /></Field>
          <div className="mf-row"><Switch checked={f.favorite} onChange={v => set('favorite', v)} label="Favourite" /><span className="grow small">Favourite: first in the list, and a message when it goes offline (if you switched that on).</span></div>
        </>}

        <button type="button" className="conn-advanced" onClick={() => setDisplay(d => !d)}><Icon name={display ? 'chevron-up' : 'chevron-down'} size={13} />Display and connection details</button>
        {display && <div className="mf-display">
          {f.desktop && <Field label="Desktop port" help={`Empty = ${DEFAULT_PORT[p]}, the usual ${PROTOCOL_TECH[p]} port.`}><input type="number" min={1} max={65535} value={f.port} onChange={e => set('port', e.target.value)} placeholder={String(DEFAULT_PORT[p])} /></Field>}
          {f.desktop && <Field group label="Colours">
            <Select ariaLabel="Colours" value={String(opt.color_depth || 0)} onChange={v => setOpt({ ...opt, color_depth: Number(v) || undefined })} options={[{ value: '0', label: 'Automatic' }, { value: '8', label: '256 colours (fastest)' }, { value: '16', label: 'High colour' }, { value: '24', label: 'True colour' }]} />
          </Field>}
          {f.desktop && p === 'rdp' && <Field group label="Security">
            <Select ariaLabel="Security" value={opt.rdp_security || 'any'} onChange={v => setOpt({ ...opt, rdp_security: v })} options={[{ value: 'any', label: 'Automatic' }, { value: 'nla', label: 'Network Level Authentication' }, { value: 'tls', label: 'TLS' }, { value: 'rdp', label: 'Standard RDP' }]} />
          </Field>}
          {f.terminal && <>
            <Field label="Terminal text size"><input type="number" min={8} max={32} value={opt.font_size || 12} onChange={e => setOpt({ ...opt, font_size: Number(e.target.value) })} /></Field>
            <Field group label="Font" help="With icons: the symbols of eza, starship and powerline prompts show properly.">
              <Select ariaLabel="Terminal font" value={opt.font_name || ''} onChange={v => setOpt({ ...opt, font_name: v || undefined })} options={[{ value: '', label: 'With icons (JetBrains Mono Nerd Font)' }, { value: 'DejaVu Sans Mono', label: 'DejaVu Sans Mono' }, { value: 'Liberation Mono', label: 'Liberation Mono' }, { value: 'Terminus', label: 'Terminus (bitmap)' }]} />
            </Field>
            <Field group label="Terminal colours"><Select ariaLabel="Terminal colours" value={opt.color_scheme || 'gray-black'} onChange={v => setOpt({ ...opt, color_scheme: v })} options={[{ value: 'gray-black', label: 'Grey on black' }, { value: 'green-black', label: 'Green on black' }, { value: 'white-black', label: 'White on black' }, { value: 'black-white', label: 'Black on white' }]} /></Field>
          </>}
          <div className="mf-switches">
            {f.desktop && p === 'rdp' && <Toggle checked={!!opt.standard_res} onChange={v => setOpt({ ...opt, standard_res: v })} label="Standard size (bigger, softer on a Retina screen)" />}
            {f.desktop && <Toggle checked={!!opt.save_data} onChange={v => setOpt({ ...opt, save_data: v })} label="Save data (softer picture, for slow connections)" />}
            <Toggle checked={!!opt.read_only} onChange={v => setOpt({ ...opt, read_only: v })} label="Watch only (no mouse or keys)" />
            {f.desktop && p === 'rdp' && <>
              <Toggle checked={!!opt.ignore_cert} onChange={v => setOpt({ ...opt, ignore_cert: v })} label="Accept the PC's own certificate" />
              <Toggle checked={!!opt.resize} onChange={v => setOpt({ ...opt, resize: v })} label="Follow the window size" />
              <Toggle checked={!!opt.audio} onChange={v => setOpt({ ...opt, audio: v })} label="Play the PC's sound here" />
            </>}
            {f.desktop && p === 'vnc' && <>
              <Toggle checked={!!opt.remote_cursor} onChange={v => setOpt({ ...opt, remote_cursor: v })} label="Draw the pointer on the computer" />
              <Toggle checked={!!opt.swap_red_blue} onChange={v => setOpt({ ...opt, swap_red_blue: v })} label="Swap red and blue (odd colours)" />
            </>}
          </div>
        </div>}
      </form>
    </Modal>
  )
}

/** A switch with its words beside it; the words toggle it too. */
function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return <label className="mf-switch"><Switch checked={checked} onChange={onChange} label={label} /><span>{label}</span></label>
}
