package cs3reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	typespb "github.com/cs3org/go-cs3apis/cs3/types/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"
	"github.com/opencloud-eu/reva/v2/pkg/utils"
	grpcmetadata "google.golang.org/grpc/metadata"
)

const (
	synaplanRoot = "Synaplan"
	maxNameTries = 50
)

// kindFolder maps a file extension to the folder Nextcloud uses under
// Synaplan/. Unknown extensions land in the Synaplan root.
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

// Save writes content into the signed-in user's personal space, under
// Synaplan/<Kind>/ when the extension is known. That folder is one
// target of SaveChild, the only write into OpenCloud. If the name is
// taken it appends " (2)", " (3)", … and returns the space-relative
// path that was written.
func (r *Reader) Save(ctx context.Context, filename string, content []byte) (string, error) {
	if r == nil || r.gws == nil {
		return "", errors.New("cs3reader: not configured")
	}
	name := path.Base(strings.TrimSpace(filename))
	if name == "" || name == "." || name == ".." {
		return "", errors.New("cs3reader: invalid filename")
	}

	accessToken, ok := revactx.ContextGetToken(ctx)
	if !ok || accessToken == "" {
		return "", errors.New("cs3reader: no reva access token in context")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return "", fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	gwCtx := grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, accessToken)

	root, err := personalRoot(gwCtx, gwc)
	if err != nil {
		return "", err
	}

	folder := synaplanRoot
	if kind := kindFolder(name); kind != "" {
		folder = synaplanRoot + "/" + kind
	}
	if err := ensureFolder(gwCtx, gwc, root, synaplanRoot); err != nil {
		return "", err
	}
	if folder != synaplanRoot {
		if err := ensureFolder(gwCtx, gwc, root, folder); err != nil {
			return "", err
		}
	}

	id, err := resourceIDAt(gwCtx, gwc, root, folder)
	if err != nil {
		return "", err
	}
	return r.SaveChild(ctx, id, name, content)
}

// ParentFolderID is the folder that contains resourceID. A summary or
// a translation is saved there, through SaveChild, so the result sits
// next to its source file in whichever space that file is in.
func (r *Reader) ParentFolderID(ctx context.Context, resourceID string) (string, error) {
	if r == nil || r.gws == nil {
		return "", errors.New("cs3reader: not configured")
	}
	accessToken, ok := revactx.ContextGetToken(ctx)
	if !ok || accessToken == "" {
		return "", errors.New("cs3reader: no reva access token in context")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return "", fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	gwCtx := grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, accessToken)

	rid, err := storagespace.ParseID(resourceID)
	if err != nil {
		return "", fmt.Errorf("cs3reader: parse resource id %q: %w", resourceID, err)
	}
	statRes, err := gwc.Stat(gwCtx, &provider.StatRequest{Ref: &provider.Reference{ResourceId: &rid}})
	if err != nil {
		return "", fmt.Errorf("cs3reader: stat: %w", err)
	}
	if statRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return "", fmt.Errorf("cs3reader: stat: %s", statRes.GetStatus().GetMessage())
	}
	info := statRes.GetInfo()
	// A parent id without its space cannot be opened again. Fall
	// through to the path in that case.
	if parent := info.GetParentId(); parent.GetOpaqueId() != "" && parent.GetSpaceId() != "" {
		return storagespace.FormatResourceID(parent), nil
	}
	root := info.GetSpace().GetRoot()
	if root == nil {
		return "", errors.New("cs3reader: stat did not return a space root")
	}
	parentPath := path.Dir(strings.Trim(utils.MakeRelativePath(info.GetPath()), "/"))
	if parentPath == "." {
		parentPath = ""
	}
	return resourceIDAt(gwCtx, gwc, root, parentPath)
}

func resourceIDAt(ctx context.Context, gwc gateway.GatewayAPIClient, root *provider.ResourceId, rel string) (string, error) {
	res, err := gwc.Stat(ctx, &provider.StatRequest{
		Ref: &provider.Reference{ResourceId: root, Path: utils.MakeRelativePath(rel)},
	})
	if err != nil {
		return "", fmt.Errorf("cs3reader: stat %s: %w", rel, err)
	}
	if res.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return "", fmt.Errorf("cs3reader: stat %s: %s", rel, res.GetStatus().GetMessage())
	}
	id := res.GetInfo().GetId()
	if id.GetOpaqueId() == "" {
		return "", fmt.Errorf("cs3reader: stat %s: no id", rel)
	}
	return storagespace.FormatResourceID(id), nil
}

func personalRoot(ctx context.Context, gwc gateway.GatewayAPIClient) (*provider.ResourceId, error) {
	user, ok := revactx.ContextGetUser(ctx)
	if !ok || user.GetId().GetOpaqueId() == "" {
		return nil, errors.New("cs3reader: no user on context")
	}
	res, err := gwc.ListStorageSpaces(ctx, &provider.ListStorageSpacesRequest{
		Filters: []*provider.ListStorageSpacesRequest_Filter{{
			Type: provider.ListStorageSpacesRequest_Filter_TYPE_SPACE_TYPE,
			Term: &provider.ListStorageSpacesRequest_Filter_SpaceType{SpaceType: "personal"},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("cs3reader: list spaces: %w", err)
	}
	if res.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return nil, fmt.Errorf("cs3reader: list spaces: %s", res.GetStatus().GetMessage())
	}
	want := user.GetId().GetOpaqueId()
	for _, space := range res.GetStorageSpaces() {
		if space.GetRoot() == nil || space.GetSpaceType() != "personal" {
			continue
		}
		if space.GetOwner().GetId().GetOpaqueId() == want {
			return space.GetRoot(), nil
		}
	}
	return nil, errors.New("cs3reader: personal space not found")
}

func ensureFolder(ctx context.Context, gwc gateway.GatewayAPIClient, root *provider.ResourceId, rel string) error {
	res, err := gwc.CreateContainer(ctx, &provider.CreateContainerRequest{
		Ref: &provider.Reference{ResourceId: root, Path: utils.MakeRelativePath(rel)},
	})
	if err != nil {
		return fmt.Errorf("cs3reader: create %s: %w", rel, err)
	}
	code := res.GetStatus().GetCode()
	if code == rpc.Code_CODE_OK || code == rpc.Code_CODE_ALREADY_EXISTS {
		return nil
	}
	return fmt.Errorf("cs3reader: create %s: %s", rel, res.GetStatus().GetMessage())
}

func freeName(ctx context.Context, gwc gateway.GatewayAPIClient, root *provider.ResourceId, folder, name string) (string, error) {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	candidate := name
	for n := 0; n < maxNameTries; n++ {
		if n > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", base, n+1, ext)
		}
		rel := folder + "/" + candidate
		taken, err := pathTaken(ctx, gwc, root, rel)
		if err != nil {
			return "", err
		}
		if !taken {
			return rel, nil
		}
	}
	return "", errors.New("cs3reader: no free filename")
}

func pathTaken(ctx context.Context, gwc gateway.GatewayAPIClient, root *provider.ResourceId, rel string) (bool, error) {
	res, err := gwc.Stat(ctx, &provider.StatRequest{
		Ref: &provider.Reference{ResourceId: root, Path: utils.MakeRelativePath(rel)},
	})
	if err != nil {
		return false, fmt.Errorf("cs3reader: stat %s: %w", rel, err)
	}
	switch res.GetStatus().GetCode() {
	case rpc.Code_CODE_NOT_FOUND:
		return false, nil
	case rpc.Code_CODE_OK:
		return true, nil
	default:
		return false, fmt.Errorf("cs3reader: stat %s: %s", rel, res.GetStatus().GetMessage())
	}
}

func (r *Reader) uploadSimple(ctx context.Context, gwc gateway.GatewayAPIClient, gwCtx context.Context, accessToken string, root *provider.ResourceId, rel string, content []byte) error {
	// Without the length on the initiate call the storage finishes the
	// upload as an empty file at once, and the PUT finds no session.
	opaque := &typespb.Opaque{}
	utils.AppendPlainToOpaque(opaque, "Upload-Length", strconv.Itoa(len(content)))
	up, err := gwc.InitiateFileUpload(gwCtx, &provider.InitiateFileUploadRequest{
		Ref:    &provider.Reference{ResourceId: root, Path: utils.MakeRelativePath(rel)},
		Opaque: opaque,
	})
	if err != nil {
		return fmt.Errorf("cs3reader: initiate upload: %w", err)
	}
	if up.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return fmt.Errorf("cs3reader: initiate upload: %s", up.GetStatus().GetMessage())
	}

	var endpoint, transferToken string
	for _, p := range up.GetProtocols() {
		if p.GetProtocol() == "simple" || p.GetProtocol() == "spaces" {
			endpoint, transferToken = p.GetUploadEndpoint(), p.GetToken()
			break
		}
	}
	if endpoint == "" {
		return errors.New("cs3reader: no supported upload protocol")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("cs3reader: build upload request: %w", err)
	}
	req.Header.Set("X-Reva-Transfer", transferToken)
	req.Header.Set(revactx.TokenHeader, accessToken)
	req.Header.Set("Upload-Length", strconv.Itoa(len(content)))
	req.ContentLength = int64(len(content))

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cs3reader: upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("cs3reader: upload returned %d: %s", resp.StatusCode, string(snippet))
	}
	return nil
}

// SaveChild writes content as a new file into the folder folderID, the
// folder the person has open. When the name is taken it appends " (2)",
// " (3)", … and returns the space-relative path that was written.
func (r *Reader) SaveChild(ctx context.Context, folderID, filename string, content []byte) (string, error) {
	if r == nil || r.gws == nil {
		return "", errors.New("cs3reader: not configured")
	}
	name := path.Base(strings.TrimSpace(filename))
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", errors.New("cs3reader: invalid filename")
	}
	accessToken, ok := revactx.ContextGetToken(ctx)
	if !ok || accessToken == "" {
		return "", errors.New("cs3reader: no reva access token in context")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return "", fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	gwCtx := grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, accessToken)

	rid, err := storagespace.ParseID(folderID)
	if err != nil {
		return "", fmt.Errorf("cs3reader: parse folder id %q: %w", folderID, err)
	}
	statRes, err := gwc.Stat(gwCtx, &provider.StatRequest{Ref: &provider.Reference{ResourceId: &rid}})
	if err != nil {
		return "", fmt.Errorf("cs3reader: stat folder: %w", err)
	}
	if statRes.GetStatus().GetCode() != rpc.Code_CODE_OK {
		return "", fmt.Errorf("cs3reader: stat folder: %s", statRes.GetStatus().GetMessage())
	}
	if statRes.GetInfo().GetType() != provider.ResourceType_RESOURCE_TYPE_CONTAINER {
		return "", errors.New("cs3reader: not a folder")
	}
	space := &provider.ResourceId{StorageId: rid.GetStorageId(), SpaceId: rid.GetSpaceId()}
	folder := strings.Trim(utils.MakeRelativePath(statRes.GetInfo().GetPath()), "/")

	rel, err := freeName(gwCtx, gwc, space, folder, name)
	if err != nil {
		return "", err
	}
	if err := r.uploadSimple(ctx, gwc, gwCtx, accessToken, space, rel, content); err != nil {
		return "", err
	}
	return strings.TrimPrefix(rel, "/"), nil
}
