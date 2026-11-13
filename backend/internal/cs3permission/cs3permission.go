// Package cs3permission asks OpenCloud whether the signed-in person holds
// a permission, through the CS3 gateway's CheckPermission. OpenCloud's
// settings service answers it from the person's role assignments, the same
// source OpenCloud's own admin pages use. The role ids are not in the reva
// token the proxy forwards, so they cannot be read from the request.
package cs3permission

import (
	"context"
	"errors"
	"fmt"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	permissions "github.com/cs3org/go-cs3apis/cs3/permissions/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"
	grpcmetadata "google.golang.org/grpc/metadata"
)

// Checker asks the CS3 gateway with the signed-in person's reva token.
type Checker struct {
	gws pool.Selectable[gateway.GatewayAPIClient]
}

// New returns a Checker that routes through the given gateway selector.
func New(gws pool.Selectable[gateway.GatewayAPIClient]) *Checker {
	return &Checker{gws: gws}
}

// Has reports whether the person on ctx holds permission. A permission the
// person does not hold is false without an error; an error means OpenCloud
// could not answer.
func (c *Checker) Has(ctx context.Context, permission string) (bool, error) {
	if c == nil || c.gws == nil {
		return false, errors.New("cs3permission: not configured")
	}
	user, ok := revactx.ContextGetUser(ctx)
	if !ok || user.GetId() == nil {
		return false, errors.New("cs3permission: no reva user in context")
	}
	token, ok := revactx.ContextGetToken(ctx)
	if !ok || token == "" {
		return false, errors.New("cs3permission: no reva access token in context")
	}
	gwc, err := c.gws.Next()
	if err != nil {
		return false, fmt.Errorf("cs3permission: gateway client: %w", err)
	}
	resp, err := gwc.CheckPermission(grpcmetadata.AppendToOutgoingContext(ctx, revactx.TokenHeader, token), &permissions.CheckPermissionRequest{
		Permission: permission,
		SubjectRef: &permissions.SubjectReference{
			Spec: &permissions.SubjectReference_UserId{UserId: user.GetId()},
		},
	})
	if err != nil {
		return false, fmt.Errorf("cs3permission: check %s: %w", permission, err)
	}
	switch resp.GetStatus().GetCode() {
	case rpc.Code_CODE_OK:
		return true, nil
	case rpc.Code_CODE_PERMISSION_DENIED, rpc.Code_CODE_NOT_FOUND:
		return false, nil
	default:
		return false, fmt.Errorf("cs3permission: check %s: %s", permission, resp.GetStatus().GetMessage())
	}
}
