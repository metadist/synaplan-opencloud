package cs3reader

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"
	"github.com/opencloud-eu/reva/v2/pkg/utils"
	grpcmetadata "google.golang.org/grpc/metadata"
)

// ErrNotFound is the gateway saying the resource is gone. Any other
// failure is not a deletion and must not be treated as one.
var ErrNotFound = errors.New("cs3reader: not found")

// Resource is a file or folder without its bytes.
type Resource struct {
	ID       string
	Name     string
	Path     string
	Etag     string
	MimeType string
	Size     uint64
	IsFolder bool
}

// StatResource stats a file or a folder.
func (r *Reader) StatResource(ctx context.Context, resourceID string) (Resource, error) {
	gwc, gwCtx, err := r.gateway(ctx)
	if err != nil {
		return Resource{}, err
	}
	rid, err := storagespace.ParseID(resourceID)
	if err != nil {
		return Resource{}, fmt.Errorf("cs3reader: parse resource id %q: %w", resourceID, err)
	}
	res, err := gwc.Stat(gwCtx, &provider.StatRequest{Ref: &provider.Reference{ResourceId: &rid}})
	if err != nil {
		return Resource{}, fmt.Errorf("cs3reader: stat: %w", err)
	}
	switch res.GetStatus().GetCode() {
	case rpc.Code_CODE_OK:
	case rpc.Code_CODE_NOT_FOUND:
		return Resource{}, ErrNotFound
	default:
		return Resource{}, fmt.Errorf("cs3reader: stat: %s", res.GetStatus().GetMessage())
	}
	return resourceOf(res.GetInfo()), nil
}

// StatPath stats a file or a folder by its path in the personal space of
// the user on ctx, the form a task's folder step names a folder in.
func (r *Reader) StatPath(ctx context.Context, relPath string) (Resource, error) {
	gwc, gwCtx, err := r.gateway(ctx)
	if err != nil {
		return Resource{}, err
	}
	root, err := personalRoot(gwCtx, gwc)
	if err != nil {
		return Resource{}, err
	}
	res, err := gwc.Stat(gwCtx, &provider.StatRequest{Ref: &provider.Reference{ResourceId: root, Path: utils.MakeRelativePath(strings.Trim(relPath, "/"))}})
	if err != nil {
		return Resource{}, fmt.Errorf("cs3reader: stat: %w", err)
	}
	switch res.GetStatus().GetCode() {
	case rpc.Code_CODE_OK:
	case rpc.Code_CODE_NOT_FOUND:
		return Resource{}, ErrNotFound
	default:
		return Resource{}, fmt.Errorf("cs3reader: stat: %s", res.GetStatus().GetMessage())
	}
	return resourceOf(res.GetInfo()), nil
}

// List returns the direct children of a folder.
func (r *Reader) List(ctx context.Context, folderID string) ([]Resource, error) {
	gwc, gwCtx, err := r.gateway(ctx)
	if err != nil {
		return nil, err
	}
	rid, err := storagespace.ParseID(folderID)
	if err != nil {
		return nil, fmt.Errorf("cs3reader: parse resource id %q: %w", folderID, err)
	}
	res, err := gwc.ListContainer(gwCtx, &provider.ListContainerRequest{Ref: &provider.Reference{ResourceId: &rid}})
	if err != nil {
		return nil, fmt.Errorf("cs3reader: list: %w", err)
	}
	switch res.GetStatus().GetCode() {
	case rpc.Code_CODE_OK:
	case rpc.Code_CODE_NOT_FOUND:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("cs3reader: list: %s", res.GetStatus().GetMessage())
	}
	out := make([]Resource, 0, len(res.GetInfos()))
	for _, info := range res.GetInfos() {
		out = append(out, resourceOf(info))
	}
	return out, nil
}

// gateway returns a gateway client and a context that carries the reva
// token of ctx to it.
func (r *Reader) gateway(ctx context.Context) (gateway.GatewayAPIClient, context.Context, error) {
	if r == nil || r.gws == nil {
		return nil, nil, errors.New("cs3reader: not configured")
	}
	accessToken, ok := revactx.ContextGetToken(ctx)
	if !ok || accessToken == "" {
		return nil, nil, errors.New("cs3reader: no reva access token in context")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return nil, nil, fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	return gwc, grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, accessToken), nil
}

func resourceOf(info *provider.ResourceInfo) Resource {
	return Resource{
		ID:       storagespace.FormatResourceID(info.GetId()),
		Name:     info.GetName(),
		Path:     strings.Trim(strings.TrimPrefix(utils.MakeRelativePath(info.GetPath()), "."), "/"),
		Etag:     info.GetEtag(),
		MimeType: info.GetMimeType(),
		Size:     info.GetSize(),
		IsFolder: info.GetType() == provider.ResourceType_RESOURCE_TYPE_CONTAINER,
	}
}
