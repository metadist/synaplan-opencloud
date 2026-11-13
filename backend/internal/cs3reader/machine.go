package cs3reader

import (
	"context"
	"errors"
	"fmt"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
)

// ErrNoMachineAuth means no machine-auth key is set, so nobody's files
// can be read without their browser session.
var ErrNoMachineAuth = errors.New("cs3reader: machine auth is not configured")

// SetMachineAuth stores OpenCloud's machine-auth key (OC_MACHINE_AUTH_API_KEY).
// An empty key turns reading without a browser session off.
func (r *Reader) SetMachineAuth(key string) {
	if r == nil {
		return
	}
	r.machineKey = key
}

// CanImpersonate reports whether a machine-auth key is set.
func (r *Reader) CanImpersonate() bool {
	return r != nil && r.gws != nil && r.machineKey != ""
}

// Impersonate returns a context that reads as the OpenCloud user whose CS3
// attribute claim ("username", "mail" or "userid") is value. The gateway
// mints a reva token for that user with OpenCloud's machine auth, the way
// OpenCloud's own services act for a user who is not signed in; the token
// carries only the owner scope of that user. Stat, Open, StatResource and
// List then work on the returned context as they do on a request's.
func (r *Reader) Impersonate(ctx context.Context, claim, value string) (context.Context, error) {
	if !r.CanImpersonate() {
		return nil, ErrNoMachineAuth
	}
	if claim == "" || value == "" {
		return nil, errors.New("cs3reader: no user to act for")
	}
	gwc, err := r.gws.Next()
	if err != nil {
		return nil, fmt.Errorf("cs3reader: gateway client: %w", err)
	}
	res, err := gwc.Authenticate(ctx, &gateway.AuthenticateRequest{
		Type:         "machine",
		ClientId:     claim + ":" + value,
		ClientSecret: r.machineKey,
	})
	if err != nil {
		return nil, fmt.Errorf("cs3reader: machine auth: %w", err)
	}
	if res.GetStatus().GetCode() != rpc.Code_CODE_OK || res.GetToken() == "" {
		return nil, fmt.Errorf("cs3reader: machine auth: %s", res.GetStatus().GetMessage())
	}
	if user := res.GetUser(); user != nil {
		ctx = revactx.ContextSetUser(ctx, user)
	}
	return revactx.ContextSetToken(ctx, res.GetToken()), nil
}
