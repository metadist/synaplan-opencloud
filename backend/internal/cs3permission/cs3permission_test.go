package cs3permission

import (
	"context"
	"errors"
	"testing"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	user "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	permissions "github.com/cs3org/go-cs3apis/cs3/permissions/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"
	"google.golang.org/grpc"
	grpcmetadata "google.golang.org/grpc/metadata"
)

type fakeGateway struct {
	gateway.GatewayAPIClient
	code rpc.Code
	err  error
	seen *permissions.CheckPermissionRequest
	auth []string
}

func (g *fakeGateway) CheckPermission(ctx context.Context, req *permissions.CheckPermissionRequest, _ ...grpc.CallOption) (*permissions.CheckPermissionResponse, error) {
	g.seen = req
	md, _ := grpcmetadata.FromOutgoingContext(ctx)
	g.auth = md.Get(revactx.TokenHeader)
	if g.err != nil {
		return nil, g.err
	}
	return &permissions.CheckPermissionResponse{Status: &rpc.Status{Code: g.code}}, nil
}

type selector struct{ client gateway.GatewayAPIClient }

func (s selector) Next(...pool.Option) (gateway.GatewayAPIClient, error) { return s.client, nil }

func signedIn() context.Context {
	ctx := revactx.ContextSetUser(context.Background(), &user.User{Id: &user.UserId{OpaqueId: "ada"}})
	return revactx.ContextSetToken(ctx, "reva-token")
}

func TestHas(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		code    rpc.Code
		err     error
		want    bool
		wantErr bool
		asked   bool
	}{
		{name: "held", ctx: signedIn(), code: rpc.Code_CODE_OK, want: true, asked: true},
		{name: "denied", ctx: signedIn(), code: rpc.Code_CODE_PERMISSION_DENIED, asked: true},
		{name: "unknown permission", ctx: signedIn(), code: rpc.Code_CODE_NOT_FOUND, asked: true},
		{name: "settings service failed", ctx: signedIn(), code: rpc.Code_CODE_INTERNAL, wantErr: true, asked: true},
		{name: "gateway down", ctx: signedIn(), err: errors.New("unavailable"), wantErr: true, asked: true},
		{name: "no user", ctx: revactx.ContextSetToken(context.Background(), "reva-token"), wantErr: true},
		{name: "no token", ctx: revactx.ContextSetUser(context.Background(), &user.User{Id: &user.UserId{OpaqueId: "ada"}}), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw := &fakeGateway{code: tt.code, err: tt.err}
			got, err := New(selector{gw}).Has(tt.ctx, "Accounts.ReadWrite")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Has = %v, want %v", got, tt.want)
			}
			if !tt.asked {
				if gw.seen != nil {
					t.Fatal("the gateway was asked without a signed-in person")
				}
				return
			}
			if gw.seen.GetPermission() != "Accounts.ReadWrite" || gw.seen.GetSubjectRef().GetUserId().GetOpaqueId() != "ada" {
				t.Fatalf("asked %+v", gw.seen)
			}
			if len(gw.auth) != 1 || gw.auth[0] != "reva-token" {
				t.Fatalf("token header = %v", gw.auth)
			}
		})
	}
}

func TestHasWithoutGateway(t *testing.T) {
	var c *Checker
	if _, err := c.Has(signedIn(), "Accounts.ReadWrite"); err == nil {
		t.Fatal("a missing checker answered")
	}
}
