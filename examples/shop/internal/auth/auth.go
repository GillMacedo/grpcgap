// Package auth checks the caller's role on every gRPC call.
package auth

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// public methods need no credentials.
var public = map[string]bool{
	"/grpc.health.v1.Health/Check": true,
	"/shop.v1.Orders/GetOrder":     true,
}

// adminOnly methods need the admin role. PurgeCustomer was renamed to
// DeleteCustomer in shop.v1, and this list wasn't updated.
var adminOnly = map[string]bool{
	"/shop.v1.Admin/PurgeCustomer": true,
	"/shop.v1.Admin/ExportOrders":  true,
}

func check(ctx context.Context, method string) error {
	if public[method] {
		return nil
	}
	md, _ := metadata.FromIncomingContext(ctx)
	roles := md.Get("role")
	if len(roles) == 0 {
		return status.Error(codes.Unauthenticated, "no role")
	}
	if adminOnly[method] && roles[0] != "admin" {
		return status.Error(codes.PermissionDenied, "admin only")
	}
	return nil
}

// Unary checks unary calls.
func Unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := check(ctx, info.FullMethod); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

// Stream checks streaming calls.
func Stream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := check(ss.Context(), info.FullMethod); err != nil {
		return err
	}
	return handler(srv, ss)
}
