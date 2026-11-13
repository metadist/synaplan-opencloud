package cs3reader

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"
	grpcmetadata "google.golang.org/grpc/metadata"
)

// ErrNoFolder means the id names no folder the person can see: it is
// gone, it is a file, or it was never theirs.
var ErrNoFolder = errors.New("cs3reader: no such folder")

// FolderPath returns the space-relative path of the folder resourceID, as
// the requesting person's session sees it.
func (r *Reader) FolderPath(ctx context.Context, resourceID string) (string, error) {
	if r == nil || r.gws == nil {
		return "", errors.New("cs3reader: not configured")
	}
	accessToken, ok := revactx.ContextGetToken(ctx)
	if !ok || accessToken == "" {
		return "", errors.New("cs3reader: no reva access token in context (missing synaplanauth.Middleware?)")
	}
	rid, err := storagespace.ParseID(resourceID)
	if err != nil {
		return "", ErrNoFolder
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return "", fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	gwCtx := grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, accessToken)
	res, err := gwc.Stat(gwCtx, &provider.StatRequest{Ref: &provider.Reference{ResourceId: &rid}})
	if err != nil {
		return "", fmt.Errorf("cs3reader: stat: %w", err)
	}
	switch res.GetStatus().GetCode() {
	case rpc.Code_CODE_OK:
	case rpc.Code_CODE_NOT_FOUND, rpc.Code_CODE_PERMISSION_DENIED:
		return "", ErrNoFolder
	default:
		return "", fmt.Errorf("cs3reader: stat: %s", res.GetStatus().GetMessage())
	}
	if res.GetInfo().GetType() != provider.ResourceType_RESOURCE_TYPE_CONTAINER {
		return "", ErrNoFolder
	}
	return spaceRelative(res.GetInfo().GetPath()), nil
}

// spaceRelative is a path inside its space as people read it: no leading
// "./" or slash, no trailing slash, and "" for the space itself.
func spaceRelative(p string) string {
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}
