// Command health is a test oracle. It is copied into examples/shop at test
// time and built against the vendored grpc-go. It starts a real server with
// the shop's services and the health service, calls SetServingStatus with the
// names given before a "--" argument, then asks the health service about each
// name given after it, and prints, as JSON, the status or code Check answered
// with. That is what a Kubernetes gRPC probe or grpc-health-probe gets.
package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"time"

	shopv1 "example.com/shop/gen/shop/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func main() {
	var set, ask []string
	target := &set
	for _, a := range os.Args[1:] {
		if a == "--" {
			target = &ask
			continue
		}
		*target = append(*target, a)
	}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	shopv1.RegisterOrdersServer(srv, shopv1.UnimplementedOrdersServer{})
	shopv1.RegisterAdminServer(srv, shopv1.UnimplementedAdminServer{})
	healthSrv := health.NewServer()
	for _, name := range set {
		healthSrv.SetServingStatus(name, healthpb.HealthCheckResponse_SERVING)
	}
	healthpb.RegisterHealthServer(srv, healthSrv)
	go srv.Serve(lis)
	defer srv.Stop()
	conn, err := grpc.NewClient("passthrough:///oracle", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	out := map[string]string{}
	for _, name := range ask {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: name})
		cancel()
		if err != nil {
			out[name] = status.Code(err).String()
			continue
		}
		out[name] = resp.GetStatus().String()
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
