# 0002: Read service configs like grpc-go, and test against grpc-go

**Decision:** grpcgap decodes service configs with `encoding/json` into types with the same names and shapes as grpc-go's, reimplements the steps of `parseServiceConfig`, and the tests compare the result with the real grpc-go for every config in `tests/testdata/configs`.

**Why:** the bugs this tool exists for are the ones gRPC accepts without an error. Which keys are ignored, how case is matched, what `null` does, and when a retry policy is checked are details of grpc-go's code, not of the gRPC service config spec. A rule written from the spec would be wrong: grpc-go accepts `MethodConfig` in any case, ignores snake_case keys that a proto3 JSON parser would accept, skips a `methodConfig` entry with no `name` without checking its retry policy, and only checks `retryThrottling` when a `methodConfig` is present. The last one was found by the oracle test, after the first version got it wrong. `service_config.go` is the same in grpc-go v1.70.0 and v1.85.0 apart from comments.

**Cost:** the example vendors grpc-go (about 17 MB) so the oracle runs offline. A grpc-go release that changes the parser needs the example's version bumped and the test run again.
