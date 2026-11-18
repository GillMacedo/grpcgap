# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- WARNING `throttling-without-retry`: a `retryThrottling` policy in a config whose `methodConfig` entries have no `retryPolicy`. gRPC consults the policy only when it retries a call, so such a setting never has any effect.

## [0.1.0] - 2026-11-17

### Added
- `grpcgap` CLI and the `checking-grpc-names-and-service-configs` skill: reads a Go program through `go list -deps -export`, collects every `grpc.ServiceDesc` in the linked packages, and checks the program's `"/package.Service/Method"` strings against them.
- Service config checks for `grpc.WithDefaultServiceConfig` arguments, service config string literals and `--config` files: methodConfig names that match no linked method, fields grpc-go ignores (a retry policy outside `methodConfig`, snake_case keys), entries with no name, configs `grpc.NewClient` rejects, `maxAttempts` above grpc-go's cap of 5, retry policies next to `grpc.WithDisableRetry()`, and duplicate fields.
- A summary of what each service config really applies to each method.
- `--dir`, `--tags`, `--config`, `--format text|json` and `--fail-on error|warning|never`, and `//grpcgap:ignore` comments.
- Refuses with exit 2 when the code doesn't build, the patterns match nothing, no package links `google.golang.org/grpc`, or a `--config` file is empty, cut off or not a JSON object.
