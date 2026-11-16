package cs3reader

import (
	"context"
	"errors"
	"net/http"
	"testing"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	userpb "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"
	"google.golang.org/grpc"
	grpcmetadata "google.golang.org/grpc/metadata"
)

// fakeGateway answers the few gateway calls the reader makes. Any other
// call panics on the nil embedded client.
type fakeGateway struct {
	gateway.GatewayAPIClient
	auth     *gateway.AuthenticateResponse
	stats    []*provider.StatResponse
	list     *provider.ListContainerResponse
	spaces   *provider.ListStorageSpacesResponse
	download *gateway.InitiateFileDownloadResponse
	gotAuth  *gateway.AuthenticateRequest
	gotRef   *provider.Reference
	gotToken string
}

func (f *fakeGateway) Authenticate(_ context.Context, in *gateway.AuthenticateRequest, _ ...grpc.CallOption) (*gateway.AuthenticateResponse, error) {
	f.gotAuth = in
	return f.auth, nil
}

func (f *fakeGateway) Stat(ctx context.Context, in *provider.StatRequest, _ ...grpc.CallOption) (*provider.StatResponse, error) {
	f.remember(ctx)
	f.gotRef = in.GetRef()
	res := f.stats[0]
	if len(f.stats) > 1 {
		f.stats = f.stats[1:]
	}
	return res, nil
}

func (f *fakeGateway) ListStorageSpaces(ctx context.Context, _ *provider.ListStorageSpacesRequest, _ ...grpc.CallOption) (*provider.ListStorageSpacesResponse, error) {
	f.remember(ctx)
	return f.spaces, nil
}

func (f *fakeGateway) ListContainer(ctx context.Context, _ *provider.ListContainerRequest, _ ...grpc.CallOption) (*provider.ListContainerResponse, error) {
	f.remember(ctx)
	return f.list, nil
}

func (f *fakeGateway) InitiateFileDownload(ctx context.Context, _ *provider.InitiateFileDownloadRequest, _ ...grpc.CallOption) (*gateway.InitiateFileDownloadResponse, error) {
	f.remember(ctx)
	return f.download, nil
}

func (f *fakeGateway) remember(ctx context.Context) {
	md, _ := grpcmetadata.FromOutgoingContext(ctx)
	if values := md.Get(revactx.TokenHeader); len(values) > 0 {
		f.gotToken = values[0]
	}
}

type fakeSelector struct{ gw *fakeGateway }

func (s fakeSelector) Next(...pool.Option) (gateway.GatewayAPIClient, error) { return s.gw, nil }

func readerWith(gw *fakeGateway, key string) *Reader {
	r := &Reader{gws: fakeSelector{gw}, httpClient: &http.Client{}}
	r.SetMachineAuth(key)
	return r
}

func status(code rpc.Code) *rpc.Status { return &rpc.Status{Code: code, Message: code.String()} }

func TestImpersonate(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		claim     string
		value     string
		auth      *gateway.AuthenticateResponse
		wantErr   error
		wantToken string
	}{
		{
			name:      "the gateway mints a token for the person",
			key:       "machine-key",
			claim:     "username",
			value:     "ada",
			auth:      &gateway.AuthenticateResponse{Status: status(rpc.Code_CODE_OK), Token: "reva-ada", User: &userpb.User{Username: "ada"}},
			wantToken: "reva-ada",
		},
		{name: "no key", claim: "username", value: "ada", wantErr: ErrNoMachineAuth},
		{name: "no person", key: "machine-key", claim: "username", wantErr: errors.New("any")},
		{name: "unsupported user attribute", key: "machine-key", claim: "email", value: "ada@example", wantErr: errors.New("any")},
		{name: "colon in attribute is rejected", key: "machine-key", claim: "username:admin", value: "ada", wantErr: errors.New("any")},
		{
			name:    "the gateway refuses",
			key:     "machine-key",
			claim:   "username",
			value:   "ada",
			auth:    &gateway.AuthenticateResponse{Status: status(rpc.Code_CODE_UNAUTHENTICATED)},
			wantErr: errors.New("any"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := &fakeGateway{auth: tc.auth}
			ctx, err := readerWith(gw, tc.key).Impersonate(context.Background(), tc.claim, tc.value)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatal("want an error")
				}
				if errors.Is(tc.wantErr, ErrNoMachineAuth) && !errors.Is(err, ErrNoMachineAuth) {
					t.Fatalf("err %v, want ErrNoMachineAuth", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if gw.gotAuth.GetType() != "machine" || gw.gotAuth.GetClientId() != tc.claim+":"+tc.value || gw.gotAuth.GetClientSecret() != "machine-key" {
				t.Fatalf("authenticated with %+v", gw.gotAuth)
			}
			if token, _ := revactx.ContextGetToken(ctx); token != tc.wantToken {
				t.Fatalf("token %q, want %q", token, tc.wantToken)
			}
			if user, ok := revactx.ContextGetUser(ctx); !ok || user.GetUsername() != "ada" {
				t.Fatalf("user %v on the context", user)
			}
		})
	}
}

func TestStatPath(t *testing.T) {
	root := &provider.ResourceId{StorageId: "s", SpaceId: "sp", OpaqueId: "sp"}
	spaces := &provider.ListStorageSpacesResponse{Status: status(rpc.Code_CODE_OK), StorageSpaces: []*provider.StorageSpace{
		{SpaceType: "project", Root: &provider.ResourceId{StorageId: "s", SpaceId: "other", OpaqueId: "other"}, Owner: &userpb.User{Id: &userpb.UserId{OpaqueId: "ada-id"}}},
		{SpaceType: "personal", Root: &provider.ResourceId{StorageId: "s", SpaceId: "bob", OpaqueId: "bob"}, Owner: &userpb.User{Id: &userpb.UserId{OpaqueId: "bob-id"}}},
		{SpaceType: "personal", Root: root, Owner: &userpb.User{Id: &userpb.UserId{OpaqueId: "ada-id"}}},
	}}
	folder := &provider.ResourceInfo{
		Id:   &provider.ResourceId{StorageId: "s", SpaceId: "sp", OpaqueId: "f"},
		Name: "2026", Path: "./Invoices/2026", Etag: "e1",
		Type: provider.ResourceType_RESOURCE_TYPE_CONTAINER,
	}
	ada := revactx.ContextSetUser(revactx.ContextSetToken(context.Background(), "reva-ada"), &userpb.User{Id: &userpb.UserId{OpaqueId: "ada-id"}})
	cases := []struct {
		name    string
		ctx     context.Context
		stat    *provider.StatResponse
		want    Resource
		wantErr error
	}{
		{
			name: "a folder in the person's own space",
			ctx:  ada,
			stat: &provider.StatResponse{Status: status(rpc.Code_CODE_OK), Info: folder},
			want: Resource{ID: "s$sp!f", Name: "2026", Path: "Invoices/2026", Etag: "e1", IsFolder: true},
		},
		{name: "no such path", ctx: ada, stat: &provider.StatResponse{Status: status(rpc.Code_CODE_NOT_FOUND)}, wantErr: ErrNotFound},
		{name: "nobody on the context", ctx: revactx.ContextSetToken(context.Background(), "reva-ada"), wantErr: errors.New("any")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := &fakeGateway{spaces: spaces, stats: []*provider.StatResponse{tc.stat}}
			got, err := readerWith(gw, "").StatPath(tc.ctx, "/Invoices/2026/")
			if tc.wantErr != nil {
				if err == nil || (errors.Is(tc.wantErr, ErrNotFound) && !errors.Is(err, ErrNotFound)) {
					t.Fatalf("err %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if gw.gotRef.GetResourceId() != root || gw.gotRef.GetPath() != "./Invoices/2026" {
				t.Fatalf("stat asked for %v", gw.gotRef)
			}
		})
	}
}
