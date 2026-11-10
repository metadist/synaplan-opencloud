// Package davfiles reads and writes a person's files through OpenCloud's
// public APIs (WebDAV and Graph), with an OpenCloud token issued for that
// person. It reaches exactly what the person can reach, needs no access to
// OpenCloud's internal services, and works wherever OpenCloud's public URL
// does.
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
)

// File is a file as the person sees it. The caller closes Body when it is
// set.
type File struct {
	Name     string
	MimeType string
	Size     uint64
	// Etag is the version at the time of the request. Knowledge uploads
	// send it as source_etag so a later edit can be detected.
	Etag string
	// Path is the path inside its space, without a leading slash. Synaplan
	// keeps it as the original name.
	Path string
	Body io.ReadCloser
}

// ErrNotFound means the resource does not exist or the person cannot see
// it.
var ErrNotFound = errors.New("davfiles: not found")

// TokenFunc returns an OpenCloud access token for the person the request
// is made for.
type TokenFunc func(ctx context.Context) (string, error)

// Client talks to one OpenCloud.
type Client struct {
	base  string
	http  *http.Client
	token TokenFunc
}

// New returns a Client for the OpenCloud at base (its URL without a path).
func New(base string, httpClient *http.Client, token TokenFunc) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: httpClient, token: token}
}

const (
	synaplanRoot = "Synaplan"
	maxNameTries = 50
)

const propfindBody = `<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop>
<oc:name/><d:getetag/><d:resourcetype/><d:getcontenttype/><d:getcontentlength/>
</d:prop></d:propfind>`

const metaPathBody = `<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop>
<oc:meta-path-for-user/>
</d:prop></d:propfind>`

type props struct {
	Name          string `xml:"name"`
	Etag          string `xml:"getetag"`
	ContentType   string `xml:"getcontenttype"`
	ContentLength string `xml:"getcontentlength"`
	MetaPath      string `xml:"meta-path-for-user"`
	ResourceType  struct {
		Collection *struct{} `xml:"collection"`
	} `xml:"resourcetype"`
}

type multistatus struct {
	Responses []struct {
		Propstat []struct {
			Status string `xml:"status"`
			Prop   props  `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

// Stat returns what the person sees of the file resourceID, without its
// content.
func (c *Client) Stat(ctx context.Context, resourceID string) (*File, error) {
	found, err := c.propfind(ctx, c.byID(resourceID), propfindBody)
	if err != nil {
		return nil, err
	}
	if found.ResourceType.Collection != nil {
		return nil, errors.New("davfiles: resource is not a file")
	}
	meta, err := c.propfind(ctx, c.base+"/dav/meta/"+url.PathEscape(resourceID), metaPathBody)
	if err != nil {
		return nil, err
	}
	size, _ := strconv.ParseUint(found.ContentLength, 10, 64)
	return &File{
		Name:     found.Name,
		MimeType: found.ContentType,
		Size:     size,
		Etag:     found.Etag,
		Path:     strings.Trim(meta.MetaPath, "/"),
	}, nil
}

// Open stats the file resourceID and opens its content.
func (c *Client) Open(ctx context.Context, resourceID string) (*File, error) {
	file, err := c.Stat(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	res, err := c.do(ctx, http.MethodGet, c.byID(resourceID), nil, nil)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, fmt.Errorf("davfiles: download answered %d", res.StatusCode)
	}
	file.Body = res.Body
	return file, nil
}

// Save writes content into the person's personal space, under
// Synaplan/<Kind>/ when the extension is known. When the name is taken it
// tries " (2)", " (3)", … and returns the path inside the space it wrote.
func (c *Client) Save(ctx context.Context, filename string, content []byte) (string, error) {
	name := path.Base(strings.TrimSpace(filename))
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", errors.New("davfiles: invalid filename")
	}
	space, err := c.personalSpace(ctx)
	if err != nil {
		return "", err
	}
	folder := synaplanRoot
	if kind := kindFolder(name); kind != "" {
		folder += "/" + kind
	}
	dir := c.base + "/dav/spaces/" + url.PathEscape(space)
	for _, part := range strings.Split(folder, "/") {
		dir += "/" + url.PathEscape(part)
		if err := c.mkcol(ctx, dir); err != nil {
			return "", err
		}
	}
	saved, err := c.putFree(ctx, dir, name, content)
	if err != nil {
		return "", err
	}
	return folder + "/" + saved, nil
}

// kindFolder is the folder under Synaplan/ that Nextcloud uses for a file
// with this extension, or "" for the Synaplan folder itself.
func kindFolder(filename string) string {
	switch strings.ToLower(path.Ext(filename)) {
	case ".docx", ".doc", ".pptx", ".ppt", ".xlsx", ".xls", ".csv", ".pdf", ".txt", ".md", ".odt", ".rtf":
		return "Documents"
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac":
		return "Audio"
	case ".ics":
		return "Calendar"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
		return "Images"
	case ".mp4", ".webm", ".mov", ".mkv":
		return "Video"
	default:
		return ""
	}
}

func (c *Client) byID(resourceID string) string {
	return c.base + "/dav/spaces/" + url.PathEscape(resourceID)
}

func (c *Client) do(ctx context.Context, method, target string, body io.Reader, header http.Header) (*http.Response, error) {
	token, err := c.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("davfiles: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("davfiles: %w", err)
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

// propfind asks for the properties in body of the resource at target and
// returns the first set the server found.
func (c *Client) propfind(ctx context.Context, target, body string) (props, error) {
	var prop props
	res, err := c.do(ctx, "PROPFIND", target, strings.NewReader(body), http.Header{
		"Depth":        {"0"},
		"Content-Type": {"application/xml"},
	})
	if err != nil {
		return prop, err
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusMultiStatus:
	case http.StatusNotFound:
		return prop, ErrNotFound
	default:
		return prop, fmt.Errorf("davfiles: propfind answered %d", res.StatusCode)
	}
	var parsed multistatus
	if err := xml.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return prop, fmt.Errorf("davfiles: propfind: %w", err)
	}
	for _, r := range parsed.Responses {
		for _, ps := range r.Propstat {
			if strings.Contains(ps.Status, " 200 ") {
				return ps.Prop, nil
			}
		}
	}
	return prop, ErrNotFound
}

func (c *Client) personalSpace(ctx context.Context) (string, error) {
	res, err := c.do(ctx, http.MethodGet, c.base+"/graph/v1.0/me/drive", nil, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("davfiles: personal space answered %d", res.StatusCode)
	}
	var drive struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&drive); err != nil {
		return "", fmt.Errorf("davfiles: personal space: %w", err)
	}
	if drive.ID == "" {
		return "", errors.New("davfiles: no personal space")
	}
	return drive.ID, nil
}

func (c *Client) mkcol(ctx context.Context, target string) error {
	res, err := c.do(ctx, "MKCOL", target, nil, nil)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusMethodNotAllowed {
		return fmt.Errorf("davfiles: creating a folder answered %d", res.StatusCode)
	}
	return nil
}

// putFree uploads content into dir under name, or the first of
// "name (2)", "name (3)", … that is free, and returns the name it used.
// If-None-Match: * makes the server refuse a name that is taken, so two
// saves never overwrite each other.
func (c *Client) putFree(ctx context.Context, dir, name string, content []byte) (string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; n <= maxNameTries; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		res, err := c.do(ctx, http.MethodPut, dir+"/"+url.PathEscape(candidate), bytes.NewReader(content), http.Header{
			"If-None-Match": {"*"},
		})
		if err != nil {
			return "", err
		}
		_ = res.Body.Close()
		switch res.StatusCode {
		case http.StatusCreated, http.StatusNoContent, http.StatusOK:
			return candidate, nil
		case http.StatusPreconditionFailed:
			continue
		default:
			return "", fmt.Errorf("davfiles: upload answered %d", res.StatusCode)
		}
	}
	return "", errors.New("davfiles: no free filename")
}
