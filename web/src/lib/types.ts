export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'

export interface Settings {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string; week_start: number
  pin_set: boolean; pin_minutes: number; touch_mode: 'trackpad' | 'touch'; scale_mode: 'fit' | 'actual'
  keys_bar: boolean; confirm_close: boolean
  notify_sessions: boolean; notify_offline: boolean
  notify_url: string; notify_format: string; notify_configured: boolean
  files_mode: ConnMode; files_via: ConnVia; files_detail: string; files_open_url: string; files_configured: boolean
  files_url: string; files_token: string; files_public_url: string; files_folder: string
  setup_complete: boolean; base_url: string
  guacd: { ok: boolean; detail: string; addr: string }
  tailnet: boolean
}

export type Kind = 'mac' | 'windows' | 'linux' | 'other'
export type Protocol = 'vnc' | 'rdp' | 'ssh'

export interface MachineOptions {
  color_depth?: number; read_only?: boolean; rdp_security?: string; ignore_cert?: boolean; resize?: boolean; audio?: boolean
  font_size?: number; font_name?: string; color_scheme?: string; swap_red_blue?: boolean; remote_cursor?: boolean; save_data?: boolean
}

export interface TailnetDevice { id: string; name: string; dns_name: string; os: string; ips: string[]; online: boolean; last_seen?: string; self?: boolean; tags?: string[] }

export interface MachineStatus { state: 'online' | 'offline' | 'unknown'; checked_at?: string; latency_ms?: number; detail?: string; waking_since?: string }

export interface Machine {
  id: number; name: string; kind: Kind; host: string; protocol: Protocol; port: number; effective_port: number
  username: string; domain: string; mac_address: string; wol_broadcast: string
  files_enabled: boolean; ssh_port: number; host_key: string; options: MachineOptions; notes: string
  favorite: boolean; sort: number; tailnet_id: string; last_used_at: string; last_online_at: string
  created_at: string; updated_at: string
  has_password: boolean; has_key: boolean; status: MachineStatus; tailnet?: TailnetDevice; live: number
}

export interface RemoteSession {
  id: number; machine_id: number | null; machine_name: string; protocol: Protocol; started_at: string; ended_at: string
  duration_s: number; device: string; ip: string; end_reason: string; bytes_in: number; bytes_out: number
}
export interface LiveSession { id: number; machine_id: number; machine: string; device: string; started_at: string }
export interface Transfer { id: number; machine_id: number | null; machine_name: string; direction: 'download' | 'upload' | 'to_files'; path: string; name: string; size: number; status: 'done' | 'failed'; error: string; target: string; created_at: string }
export interface Stats { sessions: number; seconds: number; machines: number }
export interface Overview { machines: number; online: number; recent: RemoteSession[]; week: Stats; month: Stats; live: LiveSession[]; guacd: { ok: boolean; detail: string; addr: string } }

export interface FileEntry { name: string; path: string; dir: boolean; size: number; modified: string; hidden: boolean }
export interface Listing { path: string; parent: string; home: string; entries: FileEntry[] }

export interface TailnetListDevice extends TailnetDevice { added_machine_id?: number; kind: Kind; protocol: Protocol; fit: boolean }
