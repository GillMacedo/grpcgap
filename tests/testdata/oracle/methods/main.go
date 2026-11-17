// Command methods is a test oracle. It is copied into examples/shop at
// test time and built against the vendored grpc-go. It starts a real
// server with the services the shop links, calls each method name given
// on the command line, and prints, as JSON, whether the server's
// interceptors saw a call with exactly that name. A name ending in "/" is
// a prefix: it counts as seen when a call to a real method of the linked
// services starts with it.
package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	shopv1 "example.com/shop/gen/shop/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

type recorder struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (r *recorder) add(m string) {
	r.mu.Lock()
	r.seen[m] = true
	r.mu.Unlock()
}

func main() {
	rec := &recorder{seen: map[string]bool{}}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(
		grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			rec.add(info.FullMethod)
			return h(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
			rec.add(info.FullMethod)
			return h(srv, ss)
		}),
	)
	shopv1.RegisterOrdersServer(srv, shopv1.UnimplementedOrdersServer{})
	shopv1.RegisterAdminServer(srv, shopv1.UnimplementedAdminServer{})
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go srv.Serve(lis)
	defer srv.Stop()
	conn, err := grpc.NewClient("passthrough:///oracle", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	call := func(m string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn.Invoke(ctx, m, &shopv1.DeleteCustomerResponse{}, &shopv1.DeleteCustomerResponse{})
	}
	var all []string
	for _, sd := range []grpc.ServiceDesc{shopv1.Orders_ServiceDesc, shopv1.Admin_ServiceDesc, healthpb.Health_ServiceDesc} {
		for _, m := range sd.Methods {
			all = append(all, "/"+sd.ServiceName+"/"+m.MethodName)
		}
		for _, s := range sd.Streams {
			all = append(all, "/"+sd.ServiceName+"/"+s.StreamName)
		}
	}
	out := map[string]bool{}
	for _, name := range os.Args[1:] {
		if strings.HasSuffix(name, "/") {
			continue
		}
		call(name)
		rec.mu.Lock()
		out[name] = rec.seen[name]
		rec.mu.Unlock()
	}
	for _, m := range all {
		call(m)
	}
	for _, name := range os.Args[1:] {
		if !strings.HasSuffix(name, "/") {
			continue
		}
		rec.mu.Lock()
		for m := range rec.seen {
			if strings.HasPrefix(m, name) {
				out[name] = true
			}
		}
		rec.mu.Unlock()
		if !out[name] {
			out[name] = false
		}
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
