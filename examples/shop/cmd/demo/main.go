// Command demo runs the shop's server and clients in one process, over an
// in-memory connection, and prints what gRPC really does with the method
// names and service configs in examples/shop.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	shopv1 "example.com/shop/gen/shop/v1"
	"example.com/shop/internal/billingclient"
	"example.com/shop/internal/ordersclient"
	"example.com/shop/internal/reportsclient"
	"example.com/shop/internal/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// flaky fails the first two attempts of every GetOrder call from each
// caller with UNAVAILABLE, the way a server does during a rollout, and
// counts attempts per caller.
type flaky struct {
	mu       sync.Mutex
	attempts map[string]int
}

func (f *flaky) intercept(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if info.FullMethod != shopv1.Orders_GetOrder_FullMethodName {
		return handler(ctx, req)
	}
	md, _ := metadata.FromIncomingContext(ctx)
	caller := md.Get("caller")[0]
	f.mu.Lock()
	f.attempts[caller]++
	n := f.attempts[caller]
	f.mu.Unlock()
	if n <= 2 {
		return nil, status.Error(codes.Unavailable, "restarting")
	}
	return handler(ctx, req)
}

func times(n int) string {
	if n == 1 {
		return "1 attempt"
	}
	return fmt.Sprintf("%d attempts", n)
}

func main() {
	lis := bufconn.Listen(1 << 20)
	f := &flaky{attempts: map[string]int{}}
	srv := server.New(f.intercept)
	go srv.Serve(lis)
	defer srv.Stop()
	dialer := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })

	clients := []struct {
		name string
		opts []grpc.DialOption
	}{
		{"checkout", ordersclient.DialOptions()},
		{"billing", billingclient.DialOptions()},
		{"reports", reportsclient.DialOptions()},
	}
	for _, c := range clients {
		conn, err := grpc.NewClient("passthrough:///shop", append(c.opts, dialer)...)
		if err != nil {
			log.Fatal(err)
		}
		ctx := metadata.AppendToOutgoingContext(context.Background(), "role", "user", "caller", c.name)
		_, err = shopv1.NewOrdersClient(conn).GetOrder(ctx, &shopv1.GetOrderRequest{Id: "o-1"})
		f.mu.Lock()
		n := f.attempts[c.name]
		f.mu.Unlock()
		fmt.Printf("%s: GetOrder while the server restarts: %s after %s\n", c.name, status.Code(err), times(n))
		conn.Close()
	}

	conn, err := grpc.NewClient("passthrough:///shop", append(ordersclient.DialOptions(), dialer)...)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx := metadata.AppendToOutgoingContext(context.Background(), "role", "user")
	_, err = shopv1.NewAdminClient(conn).DeleteCustomer(ctx, &shopv1.DeleteCustomerRequest{CustomerId: "c-1"})
	fmt.Printf("api: DeleteCustomer as role=user: %s\n", status.Code(err))
	stream, err := shopv1.NewAdminClient(conn).ExportOrders(ctx, &shopv1.ExportOrdersRequest{})
	if err == nil {
		_, err = stream.Recv()
	}
	fmt.Printf("api: ExportOrders as role=user: %s\n", status.Code(err))
}
