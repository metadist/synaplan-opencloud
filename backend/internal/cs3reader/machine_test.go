package cs3reader

import (
	"context"
	"errors"
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
	stat     *provider.StatResponse
	list     *provider.ListContainerResponse
	gotAuth  *gateway.AuthenticateRequest
	gotToken string
}

func (f *fakeGateway) Authenticate(_ context.Context, in *gateway.AuthenticateRequest, _ ...grpc.CallOption) (*gateway.AuthenticateResponse, error) {
	f.gotAuth = in
	return f.auth, nil
}

func (f *fakeGateway) Stat(ctx context.Context, _ *provider.StatRequest, _ ...grpc.CallOption) (*provider.StatResponse, error) {
	f.remember(ctx)
	return f.stat, nil
}

func (f *fakeGateway) ListContainer(ctx context.Context, _ *provider.ListContainerRequest, _ ...grpc.CallOption) (*provider.ListContainerResponse, error) {
	f.remember(ctx)
	return f.list, nil
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
	r := &Reader{gws: fakeSelector{gw}}
	r.SetMachineAuth(key)
	return r
}

func status(code rpc.Code) *rpc.Status { return &rpc.Status{Code: code, Message: code.String()} }

func TestImpersonate(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		value     string
		auth      *gateway.AuthenticateResponse
		wantErr   error
		wantToken string
	}{
		{
			name:      "the gateway mints a token for the person",
			key:       "machine-key",
			value:     "ada",
			auth:      &gateway.AuthenticateResponse{Status: status(rpc.Code_CODE_OK), Token: "reva-ada", User: &userpb.User{Username: "ada"}},
			wantToken: "reva-ada",
		},
		{name: "no key", value: "ada", wantErr: ErrNoMachineAuth},
		{name: "no person", key: "machine-key", wantErr: errors.New("any")},
		{
			name:    "the gateway refuses",
			key:     "machine-key",
			value:   "ada",
			auth:    &gateway.AuthenticateResponse{Status: status(rpc.Code_CODE_UNAUTHENTICATED)},
			wantErr: errors.New("any"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := &fakeGateway{auth: tc.auth}
			ctx, err := readerWith(gw, tc.key).Impersonate(context.Background(), "username", tc.value)
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
			if gw.gotAuth.GetType() != "machine" || gw.gotAuth.GetClientId() != "username:ada" || gw.gotAuth.GetClientSecret() != "machine-key" {
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

func TestStatResourceAndList(t *testing.T) {
	folder := &provider.ResourceInfo{
		Id:   &provider.ResourceId{StorageId: "s", SpaceId: "sp", OpaqueId: "f"},
		Name: "Inbox", Path: "./Inbox", Etag: "e1",
		Type: provider.ResourceType_RESOURCE_TYPE_CONTAINER,
	}
	file := &provider.ResourceInfo{
		Id:   &provider.ResourceId{StorageId: "s", SpaceId: "sp", OpaqueId: "a"},
		Name: "a.txt", Path: "./Inbox/a.txt", Etag: "e2", MimeType: "text/plain", Size: 5,
		Type: provider.ResourceType_RESOURCE_TYPE_FILE,
	}
	ctx := revactx.ContextSetToken(context.Background(), "reva-ada")

	t.Run("stat a folder", func(t *testing.T) {
		gw := &fakeGateway{stat: &provider.StatResponse{Status: status(rpc.Code_CODE_OK), Info: folder}}
		got, err := readerWith(gw, "").StatResource(ctx, "s$sp!f")
		if err != nil {
			t.Fatal(err)
		}
		want := Resource{ID: "s$sp!f", Name: "Inbox", Path: "Inbox", Etag: "e1", IsFolder: true}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		if gw.gotToken != "reva-ada" {
			t.Fatalf("the gateway got token %q", gw.gotToken)
		}
	})

	t.Run("a gone resource is ErrNotFound", func(t *testing.T) {
		gw := &fakeGateway{stat: &provider.StatResponse{Status: status(rpc.Code_CODE_NOT_FOUND)}}
		if _, err := readerWith(gw, "").StatResource(ctx, "s$sp!f"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err %v, want ErrNotFound", err)
		}
	})

	t.Run("any other failure is not a deletion", func(t *testing.T) {
		gw := &fakeGateway{list: &provider.ListContainerResponse{Status: status(rpc.Code_CODE_INTERNAL)}}
		if _, err := readerWith(gw, "").List(ctx, "s$sp!f"); err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("err %v, want a failure that is not ErrNotFound", err)
		}
	})

	t.Run("list a folder", func(t *testing.T) {
		gw := &fakeGateway{list: &provider.ListContainerResponse{Status: status(rpc.Code_CODE_OK), Infos: []*provider.ResourceInfo{file}}}
		got, err := readerWith(gw, "").List(ctx, "s$sp!f")
		if err != nil {
			t.Fatal(err)
		}
		want := Resource{ID: "s$sp!a", Name: "a.txt", Path: "Inbox/a.txt", Etag: "e2", MimeType: "text/plain", Size: 5}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("got %+v, want [%+v]", got, want)
		}
	})

	t.Run("without a reva token nothing is asked", func(t *testing.T) {
		if _, err := readerWith(&fakeGateway{}, "").List(context.Background(), "s$sp!f"); err == nil {
			t.Fatal("want an error")
		}
	})
}
