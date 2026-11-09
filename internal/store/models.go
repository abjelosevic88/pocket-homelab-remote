package store

import "encoding/json"

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Machine is a computer a person reaches from here. Secret and PrivateKey
// hold sealed values (see internal/seal) and are never sent to a browser.
type Machine struct {
	ID       int64  `json:"id"`
	UserID   int64  `json:"-"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Protocol string `json:"protocol"` // the desktop's: vnc | rdp
	Port     int    `json:"port"`     // the desktop's, 0 = usual
	// Ways in: the desktop (Protocol, Port) and the terminal (SSH on SSHPort,
	// the same sign-in as files).
	DesktopEnabled  bool            `json:"desktop_enabled"`
	TerminalEnabled bool            `json:"terminal_enabled"`
	Username        string          `json:"username"`
	Secret          string          `json:"-"`
	PrivateKey      string          `json:"-"`
	Domain          string          `json:"domain"`
	MACAddress      string          `json:"mac_address"`
	WOLBroadcast    string          `json:"wol_broadcast"`
	FilesEnabled    bool            `json:"files_enabled"`
	SSHPort         int             `json:"ssh_port"`
	HostKey         string          `json:"host_key"`
	Options         json.RawMessage `json:"options"`
	Notes           string          `json:"notes"`
	Favorite        bool            `json:"favorite"`
	Sort            int             `json:"sort"`
	TailnetID       string          `json:"tailnet_id"`
	LastUsedAt      string          `json:"last_used_at"`
	LastOnlineAt    string          `json:"last_online_at"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

// DefaultPort is the usual port of a protocol.
func DefaultPort(protocol string) int {
	switch protocol {
	case "rdp":
		return 3389
	case "ssh":
		return 22
	}
	return 5900
}

// EffectivePort is the machine's port, or the protocol's usual one.
func (m Machine) EffectivePort() int {
	if m.Port > 0 {
		return m.Port
	}
	return DefaultPort(m.Protocol)
}

// Ways in.
const (
	WayDesktop  = "desktop"
	WayTerminal = "terminal"
)

// ForWay is the machine as one way in sees it: for the terminal, protocol ssh
// on the SSH port; for the desktop, the machine itself.
func (m Machine) ForWay(way string) Machine {
	if way == WayTerminal {
		m.Protocol, m.Port = "ssh", m.SSHPort
	}
	return m
}

// Ways lists the ways in that are switched on, desktop first.
func (m Machine) Ways() []string {
	var w []string
	if m.DesktopEnabled {
		w = append(w, WayDesktop)
	}
	if m.TerminalEnabled {
		w = append(w, WayTerminal)
	}
	return w
}

// MachineOptions are the per-machine display and protocol choices.
type MachineOptions struct {
	ColorDepth   int    `json:"color_depth,omitempty"`   // 8 | 16 | 24 | 32, 0 = automatic
	ReadOnly     bool   `json:"read_only,omitempty"`     // watch only, no mouse or keys
	RDPSecurity  string `json:"rdp_security,omitempty"`  // any | nla | tls | rdp
	IgnoreCert   bool   `json:"ignore_cert,omitempty"`   // accept the PC's own certificate
	Resize       bool   `json:"resize,omitempty"`        // RDP: follow the window size
	Audio        bool   `json:"audio,omitempty"`         // RDP: play the PC's sound here
	FontSize     int    `json:"font_size,omitempty"`     // terminal
	FontName     string `json:"font_name,omitempty"`     // terminal: "" = the Nerd Font in our guacd image
	ColorScheme  string `json:"color_scheme,omitempty"`  // terminal: gray-black | black-white | white-black | green-black
	SwapRedBlue  bool   `json:"swap_red_blue,omitempty"` // VNC servers that send BGR
	RemoteCursor bool   `json:"remote_cursor,omitempty"` // VNC: draw the cursor on the machine
	SaveData     bool   `json:"save_data,omitempty"`     // allow JPEG/WebP for busy parts: less data, a softer picture
	StandardRes  bool   `json:"standard_res,omitempty"`  // RDP: the desktop at the window's size in points (bigger, softer on Retina) instead of every screen pixel
}

// ParseOptions reads a machine's options (unknown or broken JSON = defaults).
func ParseOptions(raw json.RawMessage) MachineOptions {
	var o MachineOptions
	_ = json.Unmarshal(raw, &o)
	return o
}

// RemoteSession is one remote session in the log.
type RemoteSession struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"-"`
	MachineID   *int64 `json:"machine_id"`
	MachineName string `json:"machine_name"`
	Protocol    string `json:"protocol"`
	StartedAt   string `json:"started_at"`
	EndedAt     string `json:"ended_at"`
	DurationS   int    `json:"duration_s"`
	Device      string `json:"device"`
	IP          string `json:"ip"`
	EndReason   string `json:"end_reason"`
	BytesIn     int64  `json:"bytes_in"`
	BytesOut    int64  `json:"bytes_out"`
}

// Transfer is a file copied to or from a machine.
type Transfer struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"-"`
	MachineID   *int64 `json:"machine_id"`
	MachineName string `json:"machine_name"`
	Direction   string `json:"direction"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	Target      string `json:"target"`
	CreatedAt   string `json:"created_at"`
}

// Settings are the box-wide values plus one person's preferences and their
// own connections. Connection modes: "core" (an app link through Pocket
// Core), "direct" (URL + token) or "off"; "" = not chosen yet.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`
	WeekStart  int    `json:"week_start"` // 1 = Monday, 7 = Sunday

	// Connecting.
	PINHash      string `json:"pin_hash"`      // bcrypt of the connect PIN, "" = none
	PINMinutes   int    `json:"pin_minutes"`   // how long a PIN stays good on one device, 0 = every time
	TouchMode    string `json:"touch_mode"`    // trackpad | touch: how a finger drives the pointer on a phone
	ScaleMode    string `json:"scale_mode"`    // fit | actual
	KeysBar      bool   `json:"keys_bar"`      // show the row of special keys on phones
	ConfirmClose bool   `json:"confirm_close"` // ask before leaving a session

	// Notifications.
	NotifySessions bool   `json:"notify_sessions"` // a message whenever a session starts
	NotifyOffline  bool   `json:"notify_offline"`  // a favourite machine went offline
	NotifyURL      string `json:"notify_url"`
	NotifyFormat   string `json:"notify_format"` // apprise | ntfy | json

	// Pocket Files: where "Save to Pocket Files" puts things.
	FilesMode      string `json:"files_mode"`
	FilesURL       string `json:"files_url"`
	FilesToken     string `json:"files_token"`
	FilesPublicURL string `json:"files_public_url"`
	FilesFolder    string `json:"files_folder"`
}
