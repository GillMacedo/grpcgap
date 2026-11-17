// Package server implements the shop's gRPC services with an in-memory
// store.
package server

import (
	"context"
	"fmt"
	"sync"

	shopv1 "example.com/shop/gen/shop/v1"
	"example.com/shop/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type store struct {
	mu     sync.Mutex
	orders map[string]*shopv1.Order
}

type orders struct {
	shopv1.UnimplementedOrdersServer
	s *store
}

type admin struct {
	shopv1.UnimplementedAdminServer
	s *store
}

// New returns a server with the auth interceptors, the shop services and
// the standard health service registered. extra interceptors run after
// auth.
func New(extra ...grpc.UnaryServerInterceptor) *grpc.Server {
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(append([]grpc.UnaryServerInterceptor{auth.Unary}, extra...)...),
		grpc.StreamInterceptor(auth.Stream),
	)
	s := &store{orders: map[string]*shopv1.Order{
		"o-1": {Id: "o-1", CustomerId: "c-1", TotalCents: 1250},
	}}
	shopv1.RegisterOrdersServer(srv, orders{s: s})
	shopv1.RegisterAdminServer(srv, admin{s: s})
	healthSrv := health.NewServer()
	// The health service answers Check and Watch from this map, so each name
	// here is one a probe can ask about. The empty name is the server as a
	// whole, which NewServer already marks serving.
	healthSrv.SetServingStatus("shop.v1.Orders", healthpb.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("shop.v1.Admin", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, healthSrv)
	return srv
}

func (o orders) GetOrder(_ context.Context, r *shopv1.GetOrderRequest) (*shopv1.Order, error) {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	if ord, ok := o.s.orders[r.GetId()]; ok {
		return ord, nil
	}
	return nil, status.Errorf(codes.NotFound, "order %s", r.GetId())
}

func (o orders) CreateOrder(_ context.Context, r *shopv1.CreateOrderRequest) (*shopv1.Order, error) {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	ord := &shopv1.Order{Id: fmt.Sprintf("o-%d", len(o.s.orders)+1), CustomerId: r.GetCustomerId(), TotalCents: r.GetTotalCents()}
	o.s.orders[ord.Id] = ord
	return ord, nil
}

func (a admin) DeleteCustomer(_ context.Context, r *shopv1.DeleteCustomerRequest) (*shopv1.DeleteCustomerResponse, error) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	for id, ord := range a.s.orders {
		if ord.GetCustomerId() == r.GetCustomerId() {
			delete(a.s.orders, id)
		}
	}
	return &shopv1.DeleteCustomerResponse{}, nil
}
