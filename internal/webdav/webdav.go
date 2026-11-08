// Package webdav puts files into a WebDAV folder (Pocket Files' /dav):
// the folders on the way are made as needed, an existing name gets " (2)".
package webdav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Client writes to one WebDAV root with Basic auth (the password is a token).
type Client struct {
	Root      string // e.g. https://files.example/dav
	User      string
	Password  string
	UserAgent string
	HTTP      *http.Client
}

func escape(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (c *Client) do(ctx context.Context, method, p string, body io.Reader, size int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Root, "/")+"/"+escape(p), body)
	if err != nil {
		return nil, err
	}
	if size >= 0 && body != nil {
		req.ContentLength = size
	}
	req.SetBasicAuth(c.User, c.Password)
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Minute}
	}
	return hc.Do(req)
}

// exists reports whether a path is there (HEAD, falling back to PROPFIND depth 0).
func (c *Client) exists(ctx context.Context, p string) (bool, error) {
	res, err := c.do(ctx, http.MethodHead, p, nil, -1)
	if err != nil {
		return false, err
	}
	res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return false, nil
	case res.StatusCode < 300:
		return true, nil
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return false, fmt.Errorf("Pocket Files refused (%s)", res.Status)
	}
	return false, nil
}

// MkdirAll makes every folder on the path.
func (c *Client) MkdirAll(ctx context.Context, dir string) error {
	cur := ""
	for _, seg := range strings.Split(strings.Trim(dir, "/"), "/") {
		if seg == "" {
			continue
		}
		cur = path.Join(cur, seg)
		res, err := c.do(ctx, "MKCOL", cur+"/", nil, -1)
		if err != nil {
			return err
		}
		res.Body.Close()
		// 201 made, 405 already there
		if res.StatusCode >= 300 && res.StatusCode != http.StatusMethodNotAllowed && res.StatusCode != http.StatusConflict {
			return fmt.Errorf("making folder %s: %s", cur, res.Status)
		}
	}
	return nil
}

// FreeName returns name, or "name (2).ext", … when it is taken in dir.
func (c *Client) FreeName(ctx context.Context, dir, name string) (string, error) {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 200; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		ok, err := c.exists(ctx, path.Join(dir, cand))
		if err != nil {
			return "", err
		}
		if !ok {
			return cand, nil
		}
	}
	return "", fmt.Errorf("too many files called %s", name)
}

// Put writes a file to dir/name (dir is made first) and returns its path.
func (c *Client) Put(ctx context.Context, dir, name string, body io.Reader, size int64) (string, error) {
	if err := c.MkdirAll(ctx, dir); err != nil {
		return "", err
	}
	free, err := c.FreeName(ctx, dir, name)
	if err != nil {
		return "", err
	}
	p := path.Join(dir, free)
	res, err := c.do(ctx, http.MethodPut, p, body, size)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return "", fmt.Errorf("Pocket Files said %s %s", res.Status, strings.TrimSpace(string(b)))
	}
	return p, nil
}
