// Package cs3reader reads files from OpenCloud storage via the CS3
// gateway on behalf of the requesting user.
//
// It takes a resource ID (the "storageId$spaceId!opaqueId" form the
// OpenCloud web frontend exposes for files), stats the resource to
// pick up its name and mime type, then initiates a download through
// the gateway and returns an io.ReadCloser streaming the bytes.
package cs3reader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"
	"github.com/opencloud-eu/reva/v2/pkg/rhttp"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"
	"github.com/opencloud-eu/reva/v2/pkg/utils"
	grpcmetadata "google.golang.org/grpc/metadata"
)

// File is a handle to a file opened via the CS3 gateway.
// The caller is responsible for closing Body.
type File struct {
	Name     string
	MimeType string
	Size     uint64
	Body     io.ReadCloser
}

// Reader opens files from the CS3 gateway.
type Reader struct {
	gws        pool.Selectable[gateway.GatewayAPIClient]
	httpClient *http.Client
}

// New creates a Reader that routes gateway calls through the given
// selector and uses an HTTP client with TLS verification according to
// insecure.
func New(gws pool.Selectable[gateway.GatewayAPIClient], insecure bool) *Reader {
	return &Reader{
		gws:        gws,
		httpClient: rhttp.GetHTTPClient(rhttp.Insecure(insecure)),
	}
}

// Open stats and downloads the file identified by resourceID. The
// reva access token must already be on ctx via revactx.ContextSetToken
// — synaplanauth.Middleware lifts it out of the x-access-token header
// the proxy sets, so handlers behind the middleware just pass their
// request context through.
//
// Callers MUST close File.Body when done.
func (r *Reader) Open(ctx context.Context, resourceID string) (*File, error) {
	accessToken, ok := revactx.ContextGetToken(ctx)
	if !ok || accessToken == "" {
		return nil, errors.New("cs3reader: no reva access token in context (missing synaplanauth.Middleware?)")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return nil, fmt.Errorf("cs3reader: gateway client: %w", err)
	}

	gwCtx := grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, accessToken)

	rid, err := storagespace.ParseID(resourceID)
	if err != nil {
		return nil, fmt.Errorf("cs3reader: parse resource id %q: %w", resourceID, err)
	}
	ref := &provider.Reference{ResourceId: &rid}

	statRes, err := gwc.Stat(gwCtx, &provider.StatRequest{Ref: ref})
	if err != nil {
		return nil, fmt.Errorf("cs3reader: stat: %w", err)
	}
	if statRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return nil, fmt.Errorf("cs3reader: stat: %s", statRes.GetStatus().GetMessage())
	}
	info := statRes.GetInfo()
	if info.GetType() != provider.ResourceType_RESOURCE_TYPE_FILE {
		return nil, errors.New("cs3reader: resource is not a file")
	}

	// InitiateFileDownload needs a path-based reference (space root +
	// relative path), not a bare resource-id ref. Stat already
	// resolved the real path for us — reuse it.
	spaceRoot := info.GetSpace().GetRoot()
	if spaceRoot == nil {
		return nil, errors.New("cs3reader: stat did not return a space root")
	}
	downloadRef := &provider.Reference{
		ResourceId: spaceRoot,
		Path:       utils.MakeRelativePath(info.GetPath()),
	}

	dlRes, err := gwc.InitiateFileDownload(gwCtx, &provider.InitiateFileDownloadRequest{Ref: downloadRef})
	if err != nil {
		return nil, fmt.Errorf("cs3reader: initiate download: %w", err)
	}
	if dlRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return nil, fmt.Errorf("cs3reader: initiate download: %s", dlRes.GetStatus().GetMessage())
	}

	var endpoint, transferToken string
	for _, p := range dlRes.GetProtocols() {
		if p.GetProtocol() == "simple" || p.GetProtocol() == "spaces" {
			endpoint, transferToken = p.GetDownloadEndpoint(), p.GetToken()
			break
		}
	}
	if endpoint == "" {
		return nil, errors.New("cs3reader: no supported download protocol")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("cs3reader: build download request: %w", err)
	}
	req.Header.Set("X-Reva-Transfer", transferToken)
	req.Header.Set(revactx.TokenHeader, accessToken)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cs3reader: download: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("cs3reader: download from %q returned %d: %s", endpoint, resp.StatusCode, string(bodySnippet))
	}

	return &File{
		Name:     info.GetName(),
		MimeType: info.GetMimeType(),
		Size:     info.GetSize(),
		Body:     resp.Body,
	}, nil
}
