# Architecture

grpcgap is one Go program in `plugin/scripts`, using only the standard library, so the installed plugin builds with `go build` and no module download.

## Flow

1. **Config files (`check.go`, `readConfigFiles`).** Each `--config` file must be one complete JSON object with at least one key. Anything else (empty, cut off, a log, `null`, an array) is refused with exit 2 before anything else runs.
2. **Load (`load.go`).** `goList` runs `go list -deps -export -json=<fields> -- <patterns>` in `--dir`. `-export` compiles every package, so a syntax or type error, a broken `go.mod` or `vendor/modules.txt`, or a missing module makes the go command fail, and grpcgap exits 2 with its first error lines. `decodeList` refuses anything that isn't whole JSON objects, a package with an `Error` or `DepsErrors`, and an empty result. If no package is `google.golang.org/grpc`, or none belongs to a main module, grpcgap refuses.
3. **Services (`scan.go`, `findServices`).** Every package that imports `google.golang.org/grpc` is parsed with `go/parser`, and every `grpc.ServiceDesc{...}` composite literal gives a service name and its method and stream names. That covers generated `_grpc.pb.go` code in the module, in dependencies and in grpc-go itself (health, reflection). A descriptor whose names aren't constants is listed in a NOTE.
4. **Your code (`scanOwn`).** In the main module's packages, skipping generated files and lines marked `//grpcgap:ignore`:
   - every string literal matching `/pkg.Service/Method` or `/pkg.Service/` is a method name reference;
   - the argument of every `grpc.WithDefaultServiceConfig` call is evaluated when it is a literal, a constant, a variable or `:=` with a constant value, or `+` of those (`constValue`). Anything else is listed as not checked;
   - every other string literal that is a JSON object with a service config key (`methodConfig`, `retryPolicy`, `loadBalancingPolicy` and the rest, in any case or with `_`) is read as a service config too.
5. **Service configs (`svcconfig.go`).** `readServiceConfig` decodes the JSON with `encoding/json` into types with the same field names and shapes as grpc-go's (`jsonSC`, `jsonMC`, `jsonName`, `jsonRetryPolicy`), with the same `Duration` and `Code` parsing, then follows the steps of grpc-go's `parseServiceConfig`: entries without `name` are skipped, retry policies are validated, names become `/service/method` paths, duplicates and empty services are errors, and retry throttling is checked only when there is a `methodConfig`. `walkIgnored` walks a parse tree with byte offsets (`jsontree.go`) to find keys that no field matches and duplicate keys; `groupIgnored` merges keys of one object that are ignored for the same reason.
6. **Findings (`check.go`).** A method name reference to an unknown service or method is an error, unless it doesn't look like a gRPC name (no package, or a lower-case service or method in an unknown package), in which case it isn't counted. Each methodConfig name is checked the same way. Each finding lists the main packages that link the package it is in.
7. **Report (`grpcgap.go`, `sarif.go`).** Text, JSON or SARIF 2.1.0. SARIF maps each finding to its rule code, error/warning level, message and source location; filenames are escaped as URI references. A healthy report has an empty results array. Exit 0, 1 by `--fail-on`, and 2 for every refusal, before any report is written.

## Why the oracle tests matter

The service config rules copy grpc-go's behavior, and grpc-go can change. `tests/live_test.go` builds `tests/testdata/oracle/svcconfig` against the grpc-go vendored in `examples/shop`, gives it every config in `tests/testdata/configs`, and checks that grpcgap agrees with `grpc.NewClient` on validity and with `ClientConn.GetMethodConfig` on every method. `tests/testdata/oracle/methods` calls each test method name on a real server and records which ones reach its interceptors.
