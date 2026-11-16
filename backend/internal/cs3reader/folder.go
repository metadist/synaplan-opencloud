package cs3reader

import (
	"context"
	"errors"
	"fmt"

	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"
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
	gwc, gwCtx, _, err := r.gateway(ctx)
	if err != nil {
		return Resource{}, err
	}
	ref, err := parseRef(resourceID)
	if err != nil {
		return Resource{}, err
	}
	res, err := gwc.Stat(gwCtx, &provider.StatRequest{Ref: ref})
	if err != nil {
		return Resource{}, fmt.Errorf("cs3reader: stat: %w", err)
	}
	if err := checkStatus("stat", res.GetStatus()); err != nil {
		return Resource{}, err
	}
	return resourceOf(res.GetInfo()), nil
}

// List returns the direct children of a folder.
func (r *Reader) List(ctx context.Context, folderID string) ([]Resource, error) {
	gwc, gwCtx, _, err := r.gateway(ctx)
	if err != nil {
		return nil, err
	}
	ref, err := parseRef(folderID)
	if err != nil {
		return nil, err
	}
	res, err := gwc.ListContainer(gwCtx, &provider.ListContainerRequest{Ref: ref})
	if err != nil {
		return nil, fmt.Errorf("cs3reader: list: %w", err)
	}
	if err := checkStatus("list", res.GetStatus()); err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(res.GetInfos()))
	for _, info := range res.GetInfos() {
		out = append(out, resourceOf(info))
	}
	return out, nil
}

func checkStatus(op string, st *rpc.Status) error {
	switch st.GetCode() {
	case rpc.Code_CODE_OK:
		return nil
	case rpc.Code_CODE_NOT_FOUND:
		return ErrNotFound
	default:
		return fmt.Errorf("cs3reader: %s: %s", op, st.GetMessage())
	}
}

func resourceOf(info *provider.ResourceInfo) Resource {
	return Resource{
		ID:       storagespace.FormatResourceID(info.GetId()),
		Name:     info.GetName(),
		Path:     relativePath(info.GetPath()),
		Etag:     info.GetEtag(),
		MimeType: info.GetMimeType(),
		Size:     info.GetSize(),
		IsFolder: info.GetType() == provider.ResourceType_RESOURCE_TYPE_CONTAINER,
	}
}
