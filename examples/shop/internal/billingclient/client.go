// Package billingclient connects the billing service to the Orders API.
package billingclient

import (
	"context"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const serviceConfig = `{
  "methodConfig": [{
    "name": [{"service": "shop.v1.Orders", "method": "GetOrder"}],
    "timeout": "2s",
    "retryPolicy": {
      "maxAttempts": 3,
      "initialBackoff": "0.01s",
      "maxBackoff": "0.1s",
      "backoffMultiplier": 2,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

// logged lists the calls billing logs.
var logged = map[string]bool{
	"/shop.v1.Orders/CreateOrder": true,
}

func logCalls(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	err := invoker(ctx, method, req, reply, cc, opts...)
	if logged[method] {
		log.Printf("%s: %v", method, err)
	}
	return err
}

// DialOptions returns the options the billing service dials with.
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithUnaryInterceptor(logCalls),
	}
}
