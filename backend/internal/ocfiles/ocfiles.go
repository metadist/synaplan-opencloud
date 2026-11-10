// Package ocfiles reads and writes a person's files through OpenCloud's
// public APIs, with an OpenCloud token issued for that person: metadata
// through the Graph API (with the generated libre-graph client), content
// and folders through plain WebDAV requests. It reaches exactly what the
// person can reach, needs no access to OpenCloud's internal services, and
// works wherever OpenCloud's URL does.
package ocfiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	libregraph "github.com/opencloud-eu/libre-graph-api-go"
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
var ErrNotFound = errors.New("ocfiles: not found")

// TokenFunc returns an OpenCloud access token for the person the request
// is made for.
type TokenFunc func(ctx context.Context) (string, error)

// Client talks to one OpenCloud.
type Client struct {
	base  string
	http  *http.Client
	graph *libregraph.APIClient
	token TokenFunc
}

// New returns a Client for the OpenCloud at base (its URL without a path).
func New(base string, httpClient *http.Client, token TokenFunc) *Client {
	base = strings.TrimRight(base, "/")
	cfg := libregraph.NewConfiguration()
	cfg.Servers = libregraph.ServerConfigurations{{URL: base + "/graph"}}
	cfg.HTTPClient = httpClient
	return &Client{base: base, http: httpClient, graph: libregraph.NewAPIClient(cfg), token: token}
}

const (
	synaplanRoot = "Synaplan"
	maxNameTries = 50
)

// Stat returns what the person sees of the file resourceID, without its
// content.
func (c *Client) Stat(ctx context.Context, resourceID string) (*File, error) {
	driveID, _, ok := strings.Cut(resourceID, "!")
	if !ok {
		return nil, errors.New("ocfiles: resource is not a file")
	}
	ctx, err := c.authorized(ctx)
	if err != nil {
		return nil, err
	}
	item, res, err := c.graph.DriveItemApi.GetDriveItemV1(ctx, driveID, resourceID).Execute()
	if err != nil {
		return nil, graphError("stat", res, err)
	}
	if item.Folder != nil || item.File == nil {
		return nil, errors.New("ocfiles: resource is not a file")
	}
	parent := ""
	if item.ParentReference != nil {
		parent = item.ParentReference.GetPath()
	}
	return &File{
		Name:     item.GetName(),
		MimeType: item.File.GetMimeType(),
		Size:     uint64(max(item.GetSize(), 0)),
		Etag:     item.GetETag(),
		Path:     strings.Trim(path.Join(parent, item.GetName()), "/"),
	}, nil
}

// Open stats the file resourceID and opens its content.
func (c *Client) Open(ctx context.Context, resourceID string) (*File, error) {
	file, err := c.Stat(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	res, err := c.do(ctx, http.MethodGet, c.base+"/dav/spaces/"+url.PathEscape(resourceID), nil, nil)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, fmt.Errorf("ocfiles: download answered %d", res.StatusCode)
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
		return "", errors.New("ocfiles: invalid filename")
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

func (c *Client) authorized(ctx context.Context) (context.Context, error) {
	token, err := c.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("ocfiles: %w", err)
	}
	return context.WithValue(ctx, libregraph.ContextAccessToken, token), nil
}

func graphError(op string, res *http.Response, err error) error {
	if res != nil && res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res != nil && res.StatusCode >= 300 {
		return fmt.Errorf("ocfiles: %s answered %d", op, res.StatusCode)
	}
	return fmt.Errorf("ocfiles: %s: %w", op, err)
}

func (c *Client) personalSpace(ctx context.Context) (string, error) {
	ctx, err := c.authorized(ctx)
	if err != nil {
		return "", err
	}
	drive, res, err := c.graph.MeDriveApi.GetHome(ctx).Execute()
	if err != nil {
		return "", graphError("personal space", res, err)
	}
	if drive.GetId() == "" {
		return "", errors.New("ocfiles: no personal space")
	}
	return drive.GetId(), nil
}

func (c *Client) do(ctx context.Context, method, target string, body io.Reader, header http.Header) (*http.Response, error) {
	token, err := c.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("ocfiles: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("ocfiles: %w", err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocfiles: %s %s: %w", method, req.URL.Path, err)
	}
	return res, nil
}

func (c *Client) mkcol(ctx context.Context, target string) error {
	res, err := c.do(ctx, "MKCOL", target, nil, nil)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusMethodNotAllowed {
		return fmt.Errorf("ocfiles: creating a folder answered %d", res.StatusCode)
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
			return "", fmt.Errorf("ocfiles: upload answered %d", res.StatusCode)
		}
	}
	return "", errors.New("ocfiles: no free filename")
}
