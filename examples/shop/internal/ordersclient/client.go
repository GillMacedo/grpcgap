// Package ordersclient connects the checkout service to the Orders API.
package ordersclient

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// serviceConfig is meant to retry calls that fail with UNAVAILABLE. It is a
// bare retry policy with no methodConfig around it, so gRPC parses it as an
// empty service config and never retries.
const serviceConfig = `{
  "retryPolicy": {
    "maxAttempts": 3,
    "initialBackoff": "0.01s",
    "maxBackoff": "0.1s",
    "backoffMultiplier": 2,
    "retryableStatusCodes": ["UNAVAILABLE"]
  }
}`

// DialOptions returns the options the checkout service dials with.
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
	}
}
