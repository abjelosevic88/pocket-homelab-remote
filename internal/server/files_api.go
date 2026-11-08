package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/webdav"
)

// ---- files on a machine ----
//
// Files go over SFTP (Remote Login on a Mac, OpenSSH on Windows and Linux)
// with the machine's own sign-in, straight from the server: browse, download
// to this device, upload from it, or save into Pocket Files. The machine's
// SSH identity is learned on first contact and checked every time after.

var errHostKeyChanged = errors.New("this machine's identity changed since Pocket Remote first met it. If you reinstalled it, choose \"Trust it again\"; otherwise do not continue")

type sftpConn struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}

func (c *sftpConn) Close() {
	if c.sftp != nil {
		c.sftp.Close()
	}
	if c.ssh != nil {
		c.ssh.Close()
	}
}

// openSFTP signs in to a person's machine.
func (s *Server) openSFTP(ctx context.Context, uid int64, m store.Machine) (*sftpConn, error) {
	if !m.FilesEnabled {
		return nil, errors.New("files are off for this machine (Edit → Files)")
	}
	if isDemoHost(m.Host) {
		return nil, errors.New("this is a demo machine; add one of your own computers to see its files")
	}
	pass, err := s.seal.Open(m.Secret)
	if err != nil {
		return nil, err
	}
	keyText, err := s.seal.Open(m.PrivateKey)
	if err != nil {
		return nil, err
	}
	var auth []ssh.AuthMethod
	if keyText != "" {
		signer, err := ssh.ParsePrivateKey([]byte(keyText))
		if err != nil {
			var pe *ssh.PassphraseMissingError
			if errors.As(err, &pe) && pass != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(keyText), []byte(pass))
			}
			if err != nil {
				return nil, fmt.Errorf("the saved private key cannot be used: %w", err)
			}
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if pass != "" {
		auth = append(auth, ssh.Password(pass), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			out := make([]string, len(qs))
			for i := range qs {
				out[i] = pass
			}
			return out, nil
		}))
	}
	if len(auth) == 0 {
		return nil, errors.New("add the machine's password or a private key (Edit → Sign-in) to reach its files")
	}
	if m.Username == "" {
		return nil, errors.New("add the user name you sign in to this machine with (Edit → Sign-in)")
	}
	learned := ""
	hostKey := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if m.HostKey == "" {
			learned = got
			return nil
		}
		if got != m.HostKey {
			return errHostKeyChanged
		}
		return nil
	}
	cfg := &ssh.ClientConfig{User: m.Username, Auth: auth, HostKeyCallback: hostKey, Timeout: 8 * time.Second}
	addr := net.JoinHostPort(m.Host, strconv.Itoa(m.SSHPort))
	d := net.Dialer{Timeout: 8 * time.Second}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s for files (is Remote Login / SSH on?): %w", m.Name, err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(nc, addr, cfg)
	if err != nil {
		nc.Close()
		if errors.Is(err, errHostKeyChanged) || strings.Contains(err.Error(), errHostKeyChanged.Error()) {
			return nil, errHostKeyChanged
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, errors.New("the machine did not accept the saved user name and password (or key)")
		}
		return nil, err
	}
	sc := ssh.NewClient(cc, chans, reqs)
	fc, err := sftp.NewClient(sc)
	if err != nil {
		sc.Close()
		return nil, fmt.Errorf("the machine does not offer files over SFTP: %w", err)
	}
	if learned != "" {
		_ = s.store.SetHostKey(context.WithoutCancel(ctx), uid, m.ID, learned)
	}
	return &sftpConn{ssh: sc, sftp: fc}, nil
}

type fileEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Hidden   bool   `json:"hidden"`
}

// machineFor loads the caller's machine for a files request.
func (s *Server) machineFor(w http.ResponseWriter, r *http.Request) (store.Machine, bool) {
	ctx := r.Context()
	m, err := s.store.GetMachine(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "machine")
		return m, false
	}
	return m, true
}

func filesErr(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	if errors.Is(err, errHostKeyChanged) {
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]any{"error": err.Error(), "host_key_changed": errors.Is(err, errHostKeyChanged)})
}

// cleanRemote tidies a path from the browser ("" = the home folder).
func cleanRemote(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "~" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	// Windows drives come back as /C:/…
	if len(p) >= 2 && p[1] == ':' {
		return "/" + path.Clean(p)
	}
	return path.Clean(p)
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	m, ok := s.machineFor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	c, err := s.openSFTP(ctx, actorID(ctx), m)
	if err != nil {
		filesErr(w, err)
		return
	}
	defer c.Close()
	dir := cleanRemote(r.URL.Query().Get("path"))
	if dir == "" {
		dir = "."
	}
	if !strings.HasPrefix(dir, "/") {
		if abs, err := c.sftp.RealPath(dir); err == nil && abs != "" {
			dir = abs
		}
	}
	infos, err := c.sftp.ReadDir(dir)
	if err != nil {
		filesErr(w, fmt.Errorf("cannot open %s: %w", dir, err))
		return
	}
	out := make([]fileEntry, 0, len(infos))
	for _, fi := range infos {
		isDir := fi.IsDir()
		if fi.Mode()&0o170000 == 0o120000 { // a link: follow it to tell folders from files
			if st, err := c.sftp.Stat(path.Join(dir, fi.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		out = append(out, fileEntry{Name: fi.Name(), Path: path.Join(dir, fi.Name()), Dir: isDir, Size: fi.Size(), Modified: fi.ModTime().UTC().Format(time.RFC3339), Hidden: strings.HasPrefix(fi.Name(), ".")})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	parent := path.Dir(dir)
	if parent == dir {
		parent = ""
	}
	home, _ := c.sftp.Getwd()
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "parent": parent, "home": home, "entries": out})
}

func (s *Server) logTransfer(ctx context.Context, uid int64, m store.Machine, dir, p string, size int64, target string, err error) {
	t := store.Transfer{MachineID: &m.ID, MachineName: m.Name, Direction: dir, Path: p, Name: path.Base(p), Size: size, Target: target}
	if err != nil {
		t.Status, t.Error = "failed", err.Error()
	}
	_ = s.store.AddTransfer(context.WithoutCancel(ctx), uid, &t)
}

func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	m, ok := s.machineFor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	p := cleanRemote(r.URL.Query().Get("path"))
	if p == "" {
		writeErr(w, http.StatusBadRequest, "which file?")
		return
	}
	c, err := s.openSFTP(ctx, uid, m)
	if err != nil {
		filesErr(w, err)
		return
	}
	defer c.Close()
	f, err := c.sftp.Open(p)
	if err != nil {
		filesErr(w, fmt.Errorf("cannot open %s: %w", p, err))
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "that is a folder")
		return
	}
	ct := mime.TypeByExtension(path.Ext(p))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	disp := "attachment"
	if queryBool(r, "inline") {
		disp = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": path.Base(p)}))
	n, err := io.Copy(w, f)
	s.logTransfer(ctx, uid, m, "download", p, n, "", err)
}

// handleUploadFile streams one file from the browser into a folder of the machine.
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	m, ok := s.machineFor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	dir := cleanRemote(r.URL.Query().Get("path"))
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.UploadMaxBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "send the file as multipart form data")
		return
	}
	c, err := s.openSFTP(ctx, uid, m)
	if err != nil {
		filesErr(w, err)
		return
	}
	defer c.Close()
	if dir == "" {
		if dir, err = c.sftp.Getwd(); err != nil {
			dir = "/"
		}
	}
	var saved []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "the upload broke off")
			return
		}
		name := path.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if part.FormName() != "file" || name == "" || name == "." || name == "/" {
			part.Close()
			continue
		}
		target := freeRemote(c.sftp, dir, name)
		f, err := c.sftp.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			part.Close()
			s.logTransfer(ctx, uid, m, "upload", target, 0, "", err)
			filesErr(w, fmt.Errorf("cannot write to %s: %w", dir, err))
			return
		}
		n, err := io.Copy(f, part)
		f.Close()
		part.Close()
		s.logTransfer(ctx, uid, m, "upload", target, n, "", err)
		if err != nil {
			_ = c.sftp.Remove(target)
			filesErr(w, fmt.Errorf("the upload of %s broke off: %w", name, err))
			return
		}
		saved = append(saved, target)
	}
	if len(saved) == 0 {
		writeErr(w, http.StatusBadRequest, "no file came with the upload")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "saved": saved})
}

// freeRemote returns dir/name, or "name (2).ext", … when it is taken.
func freeRemote(c *sftp.Client, dir, name string) string {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 500; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		p := path.Join(dir, cand)
		if _, err := c.Lstat(p); err != nil {
			return p
		}
	}
	return path.Join(dir, fmt.Sprintf("%s-%d%s", base, time.Now().Unix(), ext))
}

// handleSaveToFiles copies a file from the machine into the person's Pocket Files.
func (s *Server) handleSaveToFiles(w http.ResponseWriter, r *http.Request) {
	m, ok := s.machineFor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Path string `json:"path"`
	}
	if err := decode(r, &in); err != nil || cleanRemote(in.Path) == "" {
		writeErr(w, http.StatusBadRequest, "which file?")
		return
	}
	p := cleanRemote(in.Path)
	dav, folder, err := s.filesDAV(ctx, uid)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	c, err := s.openSFTP(ctx, uid, m)
	if err != nil {
		filesErr(w, err)
		return
	}
	defer c.Close()
	f, err := c.sftp.Open(p)
	if err != nil {
		filesErr(w, fmt.Errorf("cannot open %s: %w", p, err))
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "choose a file, not a folder")
		return
	}
	dir := path.Join(folder, safeName(m.Name))
	got, err := dav.Put(ctx, dir, path.Base(p), f, fi.Size())
	s.logTransfer(ctx, uid, m, "to_files", p, fi.Size(), got, err)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.publish(ctx, uid, "remote.file_saved", path.Base(p), map[string]any{"machine": m.Name, "path": p, "files_path": got, "size": fi.Size()})
	open := ""
	if sib := s.resolveSibling(ctx, "files"); sib != "" {
		open = strings.TrimRight(sib, "/") + "/"
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": got, "message": "Saved to Pocket Files · " + got, "open_url": open})
}

func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(s))
	if s == "" {
		return "Machine"
	}
	return s
}

// filesDAV is the person's Pocket Files over WebDAV, through their chosen
// connection, and the folder things go in.
func (s *Server) filesDAV(ctx context.Context, uid int64) (*webdav.Client, string, error) {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return nil, "", err
	}
	u := userFrom(ctx)
	if u == nil || u.ID != uid {
		u, _ = s.store.GetUser(ctx, uid)
	}
	mode, url, token, _ := connSettings(st, appFiles)
	folder := firstNonEmpty(st.FilesFolder, "Remote")
	switch s.connMode(mode, token) {
	case modeOff:
		return nil, "", errors.New("Pocket Files is switched off in Settings → Connected apps")
	case modeDirect:
		if token == "" {
			return nil, "", errors.New("Pocket Files is not set up (Settings → Connected apps)")
		}
		base := firstNonEmpty(url, s.resolveSibling(ctx, "files"))
		if base == "" {
			return nil, "", errors.New("set the Pocket Files address in Settings → Connected apps")
		}
		return &webdav.Client{Root: strings.TrimRight(base, "/") + "/dav", User: "pocket-remote", Password: token, UserAgent: s.cfg.UserAgent}, folder, nil
	}
	if !s.core.Configured() {
		return nil, "", errors.New("Pocket Files is reached through Pocket Core, and there is none on this box")
	}
	if u == nil || u.Email == "" {
		return nil, "", errors.New("Pocket Files needs your email to ask Pocket Core for you")
	}
	lt, err := s.core.ActAs(ctx, u.Email, "files", scopeFilesWrite, scopeFilesRead)
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			return nil, "", errors.New("Pocket Files: " + firstNonEmpty(linkReason("Pocket Files", le.Reason), le.Message))
		}
		return nil, "", err
	}
	if lt.URL == "" {
		return nil, "", errors.New("Pocket Core gave no address for Pocket Files")
	}
	return &webdav.Client{Root: strings.TrimRight(lt.URL, "/") + "/dav", User: "pocket-remote", Password: lt.AccessToken, UserAgent: s.cfg.UserAgent}, folder, nil
}
