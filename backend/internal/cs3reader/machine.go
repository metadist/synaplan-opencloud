package cs3reader

import (
	"context"
	"fmt"
	"path"
	"strings"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	userpb "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"
	"github.com/opencloud-eu/reva/v2/pkg/utils"
	grpcmetadata "google.golang.org/grpc/metadata"
)

// SetMachineAuth stores the key used to act as a person when no browser
// session is present. An empty key disables that path.
func (r *Reader) SetMachineAuth(key string) {
	if r == nil {
		return
	}
	r.machineKey = key
}

// Impersonate returns a context that can read and write as opaqueID.
// The machine-auth key must already be set.
func (r *Reader) Impersonate(ctx context.Context, opaqueID string) (context.Context, error) {
	if r == nil || r.gws == nil {
		return nil, fmt.Errorf("cs3reader: not configured")
	}
	if r.machineKey == "" {
		return nil, fmt.Errorf("cs3reader: machine auth is not configured")
	}
	if opaqueID == "" {
		return nil, fmt.Errorf("cs3reader: missing user id")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return nil, fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	authRes, err := gwc.Authenticate(ctx, &gateway.AuthenticateRequest{
		Type:         "machine",
		ClientId:     "userid:" + opaqueID,
		ClientSecret: r.machineKey,
	})
	if err != nil {
		return nil, fmt.Errorf("cs3reader: machine auth: %w", err)
	}
	if authRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return nil, fmt.Errorf("cs3reader: machine auth: %s", authRes.GetStatus().GetMessage())
	}
	user := authRes.GetUser()
	if user == nil || user.GetId().GetOpaqueId() == "" {
		user = &userpb.User{Id: &userpb.UserId{OpaqueId: opaqueID}}
	}
	ctx = revactx.ContextSetUser(ctx, user)
	ctx = revactx.ContextSetToken(ctx, authRes.GetToken())
	return ctx, nil
}

// Child is one entry in a folder listing.
type Child struct {
	ID       string
	Name     string
	Path     string
	Etag     string
	MimeType string
	Size     uint64
	IsFolder bool
}

// Meta is the name, path and kind of a resource, without its bytes.
type Meta struct {
	Name     string
	Path     string
	Etag     string
	MimeType string
	IsFolder bool
	Size     uint64
}

// StatAny stats a file or a folder.
func (r *Reader) StatAny(ctx context.Context, resourceID string) (Meta, error) {
	if r == nil || r.gws == nil {
		return Meta{}, fmt.Errorf("cs3reader: not configured")
	}
	accessToken, ok := revactx.ContextGetToken(ctx)
	if !ok || accessToken == "" {
		return Meta{}, fmt.Errorf("cs3reader: no reva access token in context")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return Meta{}, fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	gwCtx := grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, accessToken)
	rid, err := parseResource(resourceID)
	if err != nil {
		return Meta{}, err
	}
	res, err := gwc.Stat(gwCtx, &provider.StatRequest{Ref: &provider.Reference{ResourceId: &rid}})
	if err != nil {
		return Meta{}, fmt.Errorf("cs3reader: stat: %w", err)
	}
	if res.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return Meta{}, fmt.Errorf("cs3reader: stat: %s", res.GetStatus().GetMessage())
	}
	info := res.GetInfo()
	return Meta{
		Name:     info.GetName(),
		Path:     strings.Trim(utils.MakeRelativePath(info.GetPath()), "/"),
		Etag:     info.GetEtag(),
		MimeType: info.GetMimeType(),
		IsFolder: info.GetType() == provider.ResourceType_RESOURCE_TYPE_CONTAINER,
		Size:     info.GetSize(),
	}, nil
}

// List returns the direct children of a folder the context may read.
func (r *Reader) List(ctx context.Context, resourceID string) ([]Child, error) {
	if r == nil || r.gws == nil {
		return nil, fmt.Errorf("cs3reader: not configured")
	}
	accessToken, ok := revactx.ContextGetToken(ctx)
	if !ok || accessToken == "" {
		return nil, fmt.Errorf("cs3reader: no reva access token in context")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return nil, fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	gwCtx := grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, accessToken)
	rid, err := parseResource(resourceID)
	if err != nil {
		return nil, err
	}
	res, err := gwc.ListContainer(gwCtx, &provider.ListContainerRequest{
		Ref: &provider.Reference{ResourceId: &rid},
	})
	if err != nil {
		return nil, fmt.Errorf("cs3reader: list: %w", err)
	}
	if res.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return nil, fmt.Errorf("cs3reader: list: %s", res.GetStatus().GetMessage())
	}
	out := make([]Child, 0, len(res.GetInfos()))
	for _, info := range res.GetInfos() {
		id := ""
		if info.GetId() != nil {
			id = info.GetId().GetStorageId() + "$" + info.GetId().GetSpaceId() + "!" + info.GetId().GetOpaqueId()
		}
		out = append(out, Child{
			ID:       id,
			Name:     info.GetName(),
			Path:     strings.Trim(utils.MakeRelativePath(info.GetPath()), "/"),
			Etag:     info.GetEtag(),
			MimeType: info.GetMimeType(),
			Size:     info.GetSize(),
			IsFolder: info.GetType() == provider.ResourceType_RESOURCE_TYPE_CONTAINER,
		})
	}
	return out, nil
}

// SaveChild writes content as a new file inside the folder resourceID.
func (r *Reader) SaveChild(ctx context.Context, resourceID, filename string, content []byte) (string, error) {
	if r == nil || r.gws == nil {
		return "", fmt.Errorf("cs3reader: not configured")
	}
	name := path.Base(strings.TrimSpace(filename))
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("cs3reader: invalid filename")
	}
	accessToken, ok := revactx.ContextGetToken(ctx)
	if !ok || accessToken == "" {
		return "", fmt.Errorf("cs3reader: no reva access token in context")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return "", fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	gwCtx := grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, accessToken)
	rid, err := parseResource(resourceID)
	if err != nil {
		return "", err
	}
	statRes, err := gwc.Stat(gwCtx, &provider.StatRequest{Ref: &provider.Reference{ResourceId: &rid}})
	if err != nil {
		return "", fmt.Errorf("cs3reader: stat folder: %w", err)
	}
	if statRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return "", fmt.Errorf("cs3reader: stat folder: %s", statRes.GetStatus().GetMessage())
	}
	info := statRes.GetInfo()
	if info.GetType() != provider.ResourceType_RESOURCE_TYPE_CONTAINER {
		return "", fmt.Errorf("cs3reader: not a folder")
	}
	space := &provider.ResourceId{StorageId: rid.GetStorageId(), SpaceId: rid.GetSpaceId()}
	folder := strings.Trim(utils.MakeRelativePath(info.GetPath()), "/")
	rel, err := freeName(gwCtx, gwc, space, folder, name)
	if err != nil {
		return "", err
	}
	if err := r.uploadSimple(ctx, gwc, gwCtx, accessToken, space, rel, content); err != nil {
		return "", err
	}
	return rel, nil
}

func parseResource(resourceID string) (provider.ResourceId, error) {
	rid, err := storagespace.ParseID(resourceID)
	if err != nil {
		return provider.ResourceId{}, fmt.Errorf("cs3reader: parse resource id %q: %w", resourceID, err)
	}
	return rid, nil
}
