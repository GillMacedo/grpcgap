# 0003: Refuse input it can't trust, with exit 2

**Decision:** grpcgap exits 2 and prints nothing on stdout when the code doesn't build, when no package links grpc-go, when the patterns match nothing, and when a `--config` file is empty, cut off, not JSON, or a JSON value other than an object with at least one key. `--fail-on` doesn't change this.

**Why:** a check that says "No problems" for a failed build, or for a config file that holds an error message from `kubectl`, is worse than no check. A JSON object cut anywhere before its last brace is invalid JSON, so a cut `--config` file is always refused. Source files are compiled by `go list -export` first, so a cut Go file is refused unless it still builds; the tests cut files at every line and check that any cut that builds gives the same report.

**Not covered:** a service config string literal in Go code that isn't valid JSON is reported as a finding if it is passed to `grpc.WithDefaultServiceConfig` (gRPC rejects it too), and as a NOTE otherwise, because it may be a template filled in at run time.
