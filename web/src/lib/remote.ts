// Pocket Remote's words, icons and keys.
import type { Kind, Machine, Protocol } from './types'

export const KIND_NAME: Record<Kind, string> = { mac: 'Mac', windows: 'Windows PC', linux: 'Linux', other: 'Other' }
export const PROTOCOL_NAME: Record<Protocol, string> = { vnc: 'Screen Sharing', rdp: 'Remote Desktop', ssh: 'Terminal' }
export const PROTOCOL_TECH: Record<Protocol, string> = { vnc: 'VNC', rdp: 'RDP', ssh: 'SSH' }
export const DEFAULT_PORT: Record<Protocol, number> = { vnc: 5900, rdp: 3389, ssh: 22 }
export const DEFAULT_PROTOCOL: Record<Kind, Protocol> = { mac: 'vnc', windows: 'rdp', linux: 'rdp', other: 'vnc' }

export const kindIcon = (k: Kind) => k === 'mac' ? 'laptop' : k === 'windows' ? 'monitor' : k === 'linux' ? 'server' : 'monitor'

export type Way = 'desktop' | 'terminal'
export const WAY_NAME: Record<Way, string> = { desktop: 'Desktop', terminal: 'Terminal' }
export const WAY_ICON: Record<Way, string> = { desktop: 'monitor', terminal: 'terminal' }

/** "Online · 12 ms", "Waking…", "Offline · Screen Sharing is switched off on it" */
export function statusLine(m: Machine): string {
  const s = m.status
  if (s.waking_since && s.state !== 'online') return 'Waking up…'
  if (s.state === 'online') return s.latency_ms ? `Online · ${s.latency_ms} ms` : 'Online'
  if (s.state === 'offline') return s.detail ? `Offline · ${s.detail}` : 'Offline'
  return 'Checking…'
}

export function fmtSeconds(s: number): string {
  if (s < 60) return `${s} s`
  const m = Math.round(s / 60)
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60), r = m % 60
  return r ? `${h} h ${r} min` : `${h} h`
}

// X11 keysyms for the keys a phone keyboard lacks.
export const KEY = {
  esc: 0xff1b, tab: 0xff09, enter: 0xff0d, backspace: 0xff08, del: 0xffff, home: 0xff50, end: 0xff57, pgup: 0xff55, pgdn: 0xff56,
  left: 0xff51, up: 0xff52, right: 0xff53, down: 0xff54, shift: 0xffe1, ctrl: 0xffe3, alt: 0xffe9, meta: 0xffe7, super: 0xffeb,
  f: (n: number) => 0xffbe + n - 1,
}
