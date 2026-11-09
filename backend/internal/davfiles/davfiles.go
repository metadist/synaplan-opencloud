// Package davfiles reads and writes a person's files through OpenCloud's
// public WebDAV and Graph APIs, with a token issued for that person. It is
// the path for work done while the person is away: the service holds no
// OpenCloud machine key, so it reaches only what the person can reach.
//
// It returns the same types as cs3reader, so a handler can use either.
package davfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
)

// TokenFunc returns an OpenCloud access token for the person.
type TokenFunc func(ctx context.Context) (string, error)

// Client talks to one OpenCloud as one person.
type Client struct {
	base  string
	token TokenFunc
	http  *http.Client
}

// New returns a Client for the OpenCloud at base (scheme and host).
func New(base string, httpClient *http.Client, token TokenFunc) *Client {
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: httpClient}
}

const propfindBody = `<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop>
<oc:fileid/><oc:name/><d:getetag/><d:resourcetype/><d:getcontenttype/><d:getcontentlength/><oc:size/>
</d:prop></d:propfind>`

type multistatus struct {
	Responses []struct {
		Href     string `xml:"href"`
		Propstat []struct {
			Status string `xml:"status"`
			Prop   struct {
				FileID        string `xml:"fileid"`
				Name          string `xml:"name"`
				Etag          string `xml:"getetag"`
				ContentType   string `xml:"getcontenttype"`
				ContentLength string `xml:"getcontentlength"`
				Size          string `xml:"size"`
				MetaPath      string `xml:"meta-path-for-user"`
				ResourceType  struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

type entry struct {
	id, name, etag, mime string
	size                 uint64
	folder               bool
}

func (c *Client) do(ctx context.Context, method, rawURL string, body io.Reader, header http.Header) (*http.Response, error) {
	token, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("davfiles: %s %s: %w", method, req.URL.Path, err)
	}
	return res, nil
}

func (c *Client) space(id string) string {
	return c.base + "/dav/spaces/" + url.PathEscape(id)
}

func (c *Client) propfind(ctx context.Context, rawURL, depth string) ([]entry, error) {
	res, err := c.do(ctx, "PROPFIND", rawURL, strings.NewReader(propfindBody), http.Header{"Depth": {depth}, "Content-Type": {"application/xml"}})
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound {
		return nil, cs3reader.ErrNotFound
	}
	if res.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("davfiles: propfind answered %d", res.StatusCode)
	}
	var parsed multistatus
	if err := xml.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("davfiles: propfind: %w", err)
	}
	out := make([]entry, 0, len(parsed.Responses))
	for _, r := range parsed.Responses {
		for _, ps := range r.Propstat {
			if !strings.Contains(ps.Status, " 200 ") {
				continue
			}
			p := ps.Prop
			size, _ := strconv.ParseUint(p.ContentLength, 10, 64)
			if size == 0 {
				size, _ = strconv.ParseUint(p.Size, 10, 64)
			}
			out = append(out, entry{id: p.FileID, name: p.Name, etag: p.Etag, mime: p.ContentType, size: size, folder: p.ResourceType.Collection != nil})
		}
	}
	if len(out) == 0 {
		return nil, cs3reader.ErrNotFound
	}
	return out, nil
}

// pathOf is the person-relative path of a resource, without a leading slash.
func (c *Client) pathOf(ctx context.Context, id string) (string, error) {
	body := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop><oc:meta-path-for-user/></d:prop></d:propfind>`
	res, err := c.do(ctx, "PROPFIND", c.base+"/dav/meta/"+url.PathEscape(id), strings.NewReader(body), http.Header{"Depth": {"0"}, "Content-Type": {"application/xml"}})
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound {
		return "", cs3reader.ErrNotFound
	}
	if res.StatusCode != http.StatusMultiStatus {
		return "", fmt.Errorf("davfiles: meta answered %d", res.StatusCode)
	}
	var parsed multistatus
	if err := xml.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("davfiles: meta: %w", err)
	}
	for _, r := range parsed.Responses {
		for _, ps := range r.Propstat {
			if ps.Prop.MetaPath != "" {
				return strings.Trim(ps.Prop.MetaPath, "/"), nil
			}
		}
	}
	return "", nil
}

// StatAny stats a file or a folder by resource id.
func (c *Client) StatAny(ctx context.Context, id string) (cs3reader.Meta, error) {
	entries, err := c.propfind(ctx, c.space(id), "0")
	if err != nil {
		return cs3reader.Meta{}, err
	}
	p, err := c.pathOf(ctx, id)
	if err != nil {
		return cs3reader.Meta{}, err
	}
	e := entries[0]
	return cs3reader.Meta{Name: e.name, Path: p, Etag: e.etag, MimeType: e.mime, IsFolder: e.folder, Size: e.size}, nil
}

// List returns the direct children of a folder.
func (c *Client) List(ctx context.Context, id string) ([]cs3reader.Child, error) {
	entries, err := c.propfind(ctx, c.space(id), "1")
	if err != nil {
		return nil, err
	}
	parent, err := c.pathOf(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]cs3reader.Child, 0, len(entries))
	for _, e := range entries[1:] {
		out = append(out, cs3reader.Child{
			ID: e.id, Name: e.name, Path: strings.Trim(path.Join(parent, e.name), "/"),
			Etag: e.etag, MimeType: e.mime, Size: e.size, IsFolder: e.folder,
		})
	}
	return out, nil
}

// Open stats a file and opens its content. The caller closes Body.
func (c *Client) Open(ctx context.Context, id string) (*cs3reader.File, error) {
	meta, err := c.StatAny(ctx, id)
	if err != nil {
		return nil, err
	}
	if meta.IsFolder {
		return nil, errors.New("davfiles: is a folder")
	}
	res, err := c.do(ctx, http.MethodGet, c.space(id), nil, nil)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, fmt.Errorf("davfiles: download answered %d", res.StatusCode)
	}
	return &cs3reader.File{Name: meta.Name, MimeType: meta.MimeType, Size: meta.Size, Etag: meta.Etag, Path: meta.Path, Body: res.Body}, nil
}

// SaveChild writes content as a new file inside the folder id, under a free
// name.
func (c *Client) SaveChild(ctx context.Context, folderID, filename string, content []byte) (cs3reader.Written, error) {
	name, err := cleanName(filename)
	if err != nil {
		return cs3reader.Written{}, err
	}
	meta, err := c.StatAny(ctx, folderID)
	if err != nil {
		return cs3reader.Written{}, err
	}
	if !meta.IsFolder {
		return cs3reader.Written{}, errors.New("davfiles: not a folder")
	}
	written, err := c.putFree(ctx, c.space(folderID), name, content)
	if err != nil {
		return written, err
	}
	written.Path = strings.Trim(path.Join(meta.Path, written.Path), "/")
	return written, nil
}

// SaveToFolder writes into a folder relative to the personal space. An empty
// folder uses Synaplan/<Kind>/ the way cs3reader does.
func (c *Client) SaveToFolder(ctx context.Context, folder, filename string, content []byte) (cs3reader.Written, error) {
	name, err := cleanName(filename)
	if err != nil {
		return cs3reader.Written{}, err
	}
	root, err := c.personalSpace(ctx)
	if err != nil {
		return cs3reader.Written{}, err
	}
	target := cs3reader.TargetFolder(folder, name)
	dir := c.space(root)
	for _, part := range strings.Split(target, "/") {
		dir += "/" + url.PathEscape(part)
		res, err := c.do(ctx, "MKCOL", dir, nil, nil)
		if err != nil {
			return cs3reader.Written{}, err
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusMethodNotAllowed {
			return cs3reader.Written{}, fmt.Errorf("davfiles: mkcol answered %d", res.StatusCode)
		}
	}
	written, err := c.putFree(ctx, dir, name, content)
	if err != nil {
		return written, err
	}
	written.Path = target + "/" + written.Path
	return written, nil
}

// Save writes into Synaplan/<Kind>/ in the personal space.
func (c *Client) Save(ctx context.Context, filename string, content []byte) (cs3reader.Written, error) {
	return c.SaveToFolder(ctx, "", filename, content)
}

// putFree uploads under name, or name (2), name (3), … when taken. Path in
// the result is the chosen name only.
func (c *Client) putFree(ctx context.Context, dir, name string, content []byte) (cs3reader.Written, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= 50; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		res, err := c.do(ctx, http.MethodPut, dir+"/"+url.PathEscape(candidate), bytes.NewReader(content), http.Header{"If-None-Match": {"*"}})
		if err != nil {
			return cs3reader.Written{}, err
		}
		_ = res.Body.Close()
		switch res.StatusCode {
		case http.StatusCreated, http.StatusNoContent, http.StatusOK:
			return cs3reader.Written{Path: candidate, ID: res.Header.Get("Oc-Fileid"), Etag: res.Header.Get("Etag")}, nil
		case http.StatusPreconditionFailed:
			continue
		default:
			return cs3reader.Written{}, fmt.Errorf("davfiles: upload answered %d", res.StatusCode)
		}
	}
	return cs3reader.Written{}, errors.New("davfiles: no free name")
}

func (c *Client) personalSpace(ctx context.Context) (string, error) {
	res, err := c.do(ctx, http.MethodGet, c.base+"/graph/v1.0/me/drives?%24filter=driveType+eq+%27personal%27", nil, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("davfiles: drives answered %d", res.StatusCode)
	}
	var drives struct {
		Value []struct {
			ID        string `json:"id"`
			DriveType string `json:"driveType"`
		} `json:"value"`
	}
	if err := json.NewDecoder(res.Body).Decode(&drives); err != nil {
		return "", fmt.Errorf("davfiles: drives: %w", err)
	}
	for _, d := range drives.Value {
		if d.DriveType == "personal" {
			return d.ID, nil
		}
	}
	return "", errors.New("davfiles: no personal space")
}

func cleanName(filename string) (string, error) {
	name := path.Base(strings.TrimSpace(filename))
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", errors.New("davfiles: invalid filename")
	}
	return name, nil
}
