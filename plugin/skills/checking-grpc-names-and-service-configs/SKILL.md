---
name: checking-grpc-names-and-service-configs
description: Checks the gRPC method name strings ("/package.Service/Method") and service configs (grpc.WithDefaultServiceConfig JSON) in a Go program against the grpc.ServiceDesc of every service the program links. Finds interceptor allow-lists, auth rules, rate limits and per-method maps keyed by a method that was renamed, moved or never existed; methodConfig names that match no method, so a retryPolicy, timeout or waitForReady never applies; and service config fields grpc-go silently ignores, such as a retryPolicy without a methodConfig wrapper or snake_case keys. Use when retries don't happen, when an interceptor rule doesn't apply, after renaming or moving a proto service or method, when reviewing a PR that changes .proto files, interceptors or service configs, and before upgrading grpc-go.
---

# Checking gRPC method names and service configs in Go

grpcgap runs `go list -deps -export` on the packages you give it (default `./...`), reads every `grpc.ServiceDesc` in the linked packages (generated `_grpc.pb.go` code, the health and reflection services), and checks the program's own code against them:

- every string literal of the form `"/package.Service/Method"` or `"/package.Service/"`;
- every service config passed to `grpc.WithDefaultServiceConfig`, and any string literal that is a service config JSON object.

It reads a service config the way grpc-go does: the same field names, matched without regard to case, the same handling of unknown fields, null and duplicates, the same retry policy and duration rules. It doesn't run the program. It needs Go 1.22 or newer, and the module's dependencies in the module cache or `vendor/` (the go command downloads them if it can).

## Run it

Build it once, then run it in the module's directory:

    go build -o "${TMPDIR:-/tmp}/grpcgap" $(ls "${CLAUDE_PLUGIN_ROOT}"/scripts/*.go | grep -v _test.go)
    "${TMPDIR:-/tmp}/grpcgap" --dir . ./...

Pass the build tags CI uses with `--tags`. Add `--config file.json` for each service config that lives outside Go code (a file, a ConfigMap, a DNS TXT record). Use `--format json` for machine-readable output.

## Read the exit code first

- **0:** no errors. The last line says how many method names and service configs were checked. `Nothing checked` means the code has neither, which is not a clean bill of health for configs built at run time (see the `NOTE` lines).
- **1:** at least one ERROR (or WARNING with `--fail-on warning`).
- **2:** nothing was reported. The message says why: the code doesn't build, the patterns matched nothing, no package links `google.golang.org/grpc`, or a `--config` file is empty, cut off or not a JSON object. Fix that and run again. Never describe exit 2 as a clean result.

## Read each finding

- `unknown-method` / `unknown-service` on a string: a check keyed by it never matches. In an allow-list that means the method gets the default treatment (for an admin-only list, any user can call it). The `fix:` line names the method's real service when another service has it, or a near spelling.
- `unknown-method` / `unknown-service` on a methodConfig name: the retry policy, timeout or other settings of that entry never apply to any call.
- `ignored-field`: grpc-go ignores the key. The common ones are a retry policy at the top level instead of inside `methodConfig[].retryPolicy`, and snake_case keys.
- `unnamed-method-config` / `empty-name-list`: the entry applies to no method.
- `invalid-service-config`: `grpc.NewClient` fails at startup with `grpc: the provided default service config is invalid`.
- WARNING `retry-capped`, `retry-disabled`, `duplicate-field`, `empty-service-config`: the config does less than it says.

"What each service config applies" lists, for each config, the methods it really reaches and with what settings. Use it to answer "does my retry policy apply to X?".

## Report

1. Each finding: the place, the string or config entry, and what it means for the calls (which method loses retries, which check never runs).
2. The fix the finding gives.
3. Which programs link it (the `programs:` line).

## Don't

Don't call the code correct because grpcgap found nothing: it reads string literals and constants, not names built at run time, and it checks only names that look like gRPC names (see the README's Limits). Don't edit allow-lists or configs without asking; a stale entry can be a security decision. If retries or an interceptor rule don't apply and grpcgap reports nothing, say so and suggest opening an issue with the code.
