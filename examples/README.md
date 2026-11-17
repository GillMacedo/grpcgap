# Examples

`shop` is a made-up Go module with a gRPC API (`shop.v1.Orders` and `shop.v1.Admin`, in `proto/shop/v1/orders.proto`) and three clients, built on grpc-go v1.70.0.

| Code | What it does | What gRPC really does |
|---|---|---|
| `internal/auth` | Interceptor with an `adminOnly` map that still lists `/shop.v1.Admin/PurgeCustomer`, renamed to `DeleteCustomer` | A user with `role=user` can call `DeleteCustomer` |
| `internal/ordersclient` (used by `cmd/checkout`) | Service config with a bare `retryPolicy` and no `methodConfig` | No call is retried |
| `internal/reportsclient` (used by `cmd/reports`) | Service config naming `shop.v1.Order` (typo) and `shop.v1.Orders/ExportOrders` (a method of `Admin`) | No call is retried |
| `internal/billingclient` (used by `cmd/billing`) | Correct service config for `GetOrder`, and a correct method name in a logging filter | `GetOrder` is retried up to 3 times |

`deploy/service-config.json` is a service config file, as it might be kept in a ConfigMap, that writes its retry policy in snake_case.

`cmd/demo` runs the server and the three clients in one process over an in-memory connection, with a server that fails the first two `GetOrder` attempts of each client with `UNAVAILABLE`, and prints what happened. `cmd/api` is the server on its own.

It was made with:

    protoc -I proto --go_out=. --go_opt=module=example.com/shop --go-grpc_out=. --go-grpc_opt=module=example.com/shop proto/shop/v1/orders.proto
    go mod tidy
    go mod vendor

using protoc 28.3, protoc-gen-go v1.35.2 and protoc-gen-go-grpc v1.5.1. `vendor/` holds the dependencies, under their own licenses (Apache-2.0 for grpc-go and the genproto packages, BSD-3-Clause for protobuf-go and golang.org/x/net, x/sys and x/text), so the example and the tests run offline. Run the demo with `go -C examples/shop run ./cmd/demo`, and grpcgap with `go run ./plugin/scripts --dir examples/shop` from the repository root.
