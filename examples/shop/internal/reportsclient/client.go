// Package reportsclient connects the reports job to the shop.
package reportsclient

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// The first name has a typo in the service. ExportOrders is a method of
// shop.v1.Admin, not shop.v1.Orders, so the second name matches nothing
// either.
const serviceConfig = `{
  "methodConfig": [{
    "name": [
      {"service": "shop.v1.Order"},
      {"service": "shop.v1.Orders", "method": "ExportOrders"}
    ],
    "retryPolicy": {
      "maxAttempts": 3,
      "initialBackoff": "0.01s",
      "maxBackoff": "0.1s",
      "backoffMultiplier": 2,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

// DialOptions returns the options the reports job dials with.
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
	}
}
