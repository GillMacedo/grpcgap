# grpcgap: find gRPC method names and service configs in Go code that match nothing

[![CI](https://github.com/GillMacedo/grpcgap/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/grpcgap/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI for Go services built on grpc-go. It checks every `"/package.Service/Method"` string in your code (interceptor allow-lists, auth rules, rate limits, per-method maps) and every service config you pass to `grpc.WithDefaultServiceConfig` against the gRPC services the program really links. It reports method names that match no method, `methodConfig` names that match no call, and service config fields grpc-go silently ignores, such as a `retryPolicy` outside `methodConfig` or snake_case keys.**

In grpc-go a method name is a plain string. An interceptor sees `info.FullMethod`, and code compares it with a list: `adminOnly["/shop.v1.Admin/PurgeCustomer"]`. Rename the method in the `.proto` file, regenerate, and everything still compiles. The entry now matches nothing, and the admin check stops running for the renamed method. A service config fails the same way. grpc-go decodes it with `encoding/json` and ignores every key it doesn't know, so a retry policy written at the top level, under a snake_case key, in an entry with no `name`, or for a service name with a typo parses without an error and never retries anything. In October 2026 smartcontractkit/chainlink-common merged a fix for one of these (PR 2373): its chip ingress client had passed a bare retry policy to `grpc.WithDefaultServiceConfig`, and the PR says the client "has never performed a single gRPC-level retry".

grpcgap runs `go list -deps -export` (so the code must build), reads every `grpc.ServiceDesc` in the linked packages, from your generated `_grpc.pb.go` code to grpc-go's own health service, and checks your code's method name strings and service configs against them. It reads a service config with the same field names, case rules and steps as grpc-go, and the tests compare it with the real grpc-go on 42 configs. It doesn't run your program.

## What you can ask Claude

- "Our gRPC client has a retry policy, but calls fail on the first UNAVAILABLE. Why isn't it retrying?"
- "I renamed RPCs in our .proto files. Do any interceptor allow-lists or auth rules still use the old names?"
- "Which methods does this service config's timeout and retry policy actually apply to?"
- "Review this PR that changes shop.proto and the auth interceptor."
- "Check this repo's gRPC service configs before we upgrade grpc-go."

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/grpcgap
    claude plugin install grpcgap@grpcgap-marketplace

Or run it with Go 1.22 or newer, in your module's directory. It has no dependencies outside the standard library:

    go run github.com/GillMacedo/grpcgap/plugin/scripts@v0.1.0 ./...

## Example

`examples/shop` is a made-up module on grpc-go v1.70.0 with a `vendor/` directory, so it runs offline. Its server has an auth interceptor whose `adminOnly` list still names `PurgeCustomer`, which was renamed to `DeleteCustomer`. Its three clients use three service configs: `checkout`'s has a bare retry policy with no `methodConfig`, `reports`' names the service `shop.v1.Order` and puts `ExportOrders` under the wrong service, and `billing`'s is correct. `cmd/demo` runs all of it in one process, with a server that fails the first two attempts of each client's `GetOrder` with `UNAVAILABLE`:

    $ go -C examples/shop run ./cmd/demo
    checkout: GetOrder while the server restarts: Unavailable after 1 attempt
    billing: GetOrder while the server restarts: OK after 3 attempts
    reports: GetOrder while the server restarts: Unavailable after 1 attempt
    api: DeleteCustomer as role=user: OK
    api: ExportOrders as role=user: PermissionDenied

grpcgap finds the same three problems without running anything. `--fail-on never` keeps the exit code at 0 for this demo; without it, the run exits 1:

    $ go run ./plugin/scripts --fail-on never --dir examples/shop
    grpcgap 0.1.0: examples/shop, 5 programs linking google.golang.org/grpc v1.70.0; 3 services with 7 methods

    ERROR unknown-method  internal/auth/auth.go:22  "/shop.v1.Admin/PurgeCustomer"
      service shop.v1.Admin has no method PurgeCustomer: this string never equals the method name gRPC gives interceptors, stats handlers and Invoke, so a check or lookup keyed by it never matches
      programs: example.com/shop/cmd/api example.com/shop/cmd/demo
      fix: shop.v1.Admin's methods include DeleteCustomer, ExportOrders

    ERROR ignored-field  internal/ordersclient/client.go:13  retryPolicy
      a service config has no top-level field "retryPolicy", and grpc-go ignores fields it doesn't know, so it has no effect; it belongs in a methodConfig entry, so this retry policy is never used
      in the service config at internal/ordersclient/client.go:12
      programs: example.com/shop/cmd/checkout example.com/shop/cmd/demo
      fix: wrap it: {"methodConfig": [{"name": [{"service": "package.Service"}], "retryPolicy": {...}}]}

    ERROR unknown-service  internal/reportsclient/client.go:15  methodConfig[0] name {"service": "shop.v1.Order"}
      no linked package defines a gRPC service shop.v1.Order, so this name matches no call, and its settings (retry up to 3 attempts on UNAVAILABLE) never apply through it
      in the service config at internal/reportsclient/client.go:12
      programs: example.com/shop/cmd/demo example.com/shop/cmd/reports
      fix: did you mean "shop.v1.Orders"?

    ERROR unknown-method  internal/reportsclient/client.go:16  methodConfig[0] name {"service": "shop.v1.Orders", "method": "ExportOrders"}
      service shop.v1.Orders has no method ExportOrders, so this name matches no call, and its settings (retry up to 3 attempts on UNAVAILABLE) never apply through it
      in the service config at internal/reportsclient/client.go:12
      programs: example.com/shop/cmd/demo example.com/shop/cmd/reports
      fix: ExportOrders is a method of shop.v1.Admin: name that service instead

    What each service config applies:
      internal/billingclient/client.go:12:
        /shop.v1.Orders/GetOrder: retry up to 3 attempts on UNAVAILABLE, timeout 2s
      internal/ordersclient/client.go:12: nothing
      internal/reportsclient/client.go:12: nothing

    4 errors, 0 warnings: 5 method names and 3 service configs checked.

The billing program on its own is quiet, and the summary shows what its config does:

    $ go run ./plugin/scripts --dir examples/shop ./cmd/billing
    grpcgap 0.1.0: examples/shop, 1 program linking google.golang.org/grpc v1.70.0; 2 services with 5 methods

    What each service config applies:
      internal/billingclient/client.go:12:
        /shop.v1.Orders/GetOrder: retry up to 3 attempts on UNAVAILABLE, timeout 2s

    No problems: 1 method name and 1 service config checked against the linked services.

Service configs that live outside Go code (a file, a ConfigMap, a DNS TXT record) can be checked with `--config`. `examples/shop/deploy/service-config.json` uses snake_case for its retry policy, as a proto3 JSON parser would accept. grpc-go doesn't:

    $ go run ./plugin/scripts --fail-on never --dir examples/shop --config examples/shop/deploy/service-config.json ./cmd/billing
    grpcgap 0.1.0: examples/shop, 1 program linking google.golang.org/grpc v1.70.0; 2 services with 5 methods

    ERROR ignored-field  examples/shop/deploy/service-config.json:6  methodConfig[0].retry_policy
      grpc-go ignores "retry_policy": it reads camelCase field names, matched without regard to case, and not snake_case or kebab-case, so it has no effect
      in the service config at examples/shop/deploy/service-config.json
      fix: rename "retry_policy" to "retryPolicy"

    What each service config applies:
      internal/billingclient/client.go:12:
        /shop.v1.Orders/GetOrder: retry up to 3 attempts on UNAVAILABLE, timeout 2s
      examples/shop/deploy/service-config.json:
        /shop.v1.Admin/ExportOrders: timeout 1m0s, waitForReady true
        /shop.v1.Orders/*: timeout 3s

    1 error, 0 warnings: 1 method name and 2 service configs checked.

Add `--format json` for machine-readable output. `examples/README.md` says how the example was made.

## In CI

Run it after the build step, so a PR that renames an RPC or edits a service config fails when a name stops matching:

    - run: go build ./...
    - run: go run github.com/GillMacedo/grpcgap/plugin/scripts@v0.1.0 ./...

Pass `--tags` with the build tags your release build uses, `--config` for each service config file, and `--fail-on warning` to fail on warnings too.

## What it reads

- **`go list -deps -export -json` in `--dir` (default `.`)** for the packages given (default `./...`). `-export` makes the go command compile every package, so code that doesn't build is refused. The go command uses `vendor/` or the module cache as it would for `go build`, and may download modules that aren't there.
- **Every `grpc.ServiceDesc{...}` literal in the packages that import `google.golang.org/grpc`**, yours and your dependencies', for the service, method and stream names. These are the names gRPC dispatches and passes to interceptors as `info.FullMethod`.
- **Your own packages** (the main module, or every module of a `go.work` workspace), without test files and generated files:
  - every string literal of the form `"/package.Service/Method"` or `"/package.Service/"`;
  - the argument of every `grpc.WithDefaultServiceConfig` call, when it is a string literal, a constant, a variable or `:=` with a constant value, or a `+` of those;
  - every other string literal that is a JSON object with a service config key (`methodConfig`, `retryPolicy`, `retryThrottling`, `loadBalancingPolicy`, `loadBalancingConfig`, `healthCheckConfig`, in any case or with `_`).
- **Each `--config` file**, which must be one JSON object.

## How it decides

A string counts as a gRPC method name when its service part has a package (`shop.v1.Orders`) and either the service and method names start with a capital letter, or the package is one a linked service uses. `/api/v1/orders`, `/healthz` and `/Orders/GetOrder` (no package) aren't counted. A service config is read the way grpc-go's `service_config.go` reads it (the same in v1.70.0 and v1.85.0): keys match field names without regard to case, unknown keys are ignored, a `methodConfig` entry without `name` is skipped, and the retry policy, duration and status code rules are grpc-go's.

| Code | Severity | Meaning |
|---|---|---|
| `unknown-method` | ERROR | The service exists but has no such method. A string: a check keyed by it never matches. A `methodConfig` name: its settings never apply through it |
| `unknown-service` | ERROR | No linked package defines this service |
| `ignored-field` | ERROR | grpc-go ignores this key: a retry policy at the top level, snake_case, or a field in the wrong object |
| `unnamed-method-config` | ERROR | A `methodConfig` entry with no `name`, which grpc-go skips |
| `empty-name-list` | ERROR | A `methodConfig` entry with `"name": []`, which applies to no method |
| `invalid-service-config` | ERROR | grpc-go rejects the config. Passed to `grpc.WithDefaultServiceConfig`, `grpc.NewClient` fails with `grpc: the provided default service config is invalid` |
| `retry-capped` | WARNING | `maxAttempts` above 5, which grpc-go lowers to 5 unless the client sets `grpc.WithMaxCallAttempts` |
| `throttling-without-retry` | WARNING | `retryThrottling` set in a config whose `methodConfig` entries have no `retryPolicy`; gRPC consults the throttling policy only when it retries, so it never has an effect |
| `retry-disabled` | WARNING | A retry policy in the same dial options as `grpc.WithDisableRetry()` |
| `duplicate-field` | WARNING | The same field twice; `encoding/json` applies both in order |
| `empty-service-config` | WARNING | A valid config that sets nothing |

When a string is used on purpose for something that isn't a gRPC method, such as a name an HTTP handler shares with your rate limiter, put `//grpcgap:ignore` at the end of the line, on the line above, or in the doc comment of the `const`, `var` or `func` declaration that holds it.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | No errors (no findings with `--fail-on warning`), or `--fail-on never` |
| 1 | At least one ERROR, or one WARNING with `--fail-on warning` |
| 2 | The input can't be trusted or there is nothing to check, or the flags are wrong. Stdout is empty, whatever `--fail-on` says |

## When it refuses

grpcgap exits 2 and prints only the reason when it can't trust what it read. For example, with a type error added to `examples/shop/internal/auth/auth.go`, it prints this on stderr:

    grpcgap: can't trust the input, nothing was reported: go list -deps -export failed in /path/to/examples/shop, so the code doesn't build or the patterns are wrong: # example.com/shop/internal/auth; internal/auth/auth.go:56:13: cannot use "x" (untyped string constant) as int value in variable declaration

It does the same for a `go.mod`, `vendor/modules.txt` or Go file (yours, generated or vendored) that is empty, garbage, cut off, or replaced by a build log, a test log, a `kubectl` error or an HTML error page; a missing import; missing modules with no way to download them; a directory that doesn't exist or has no `go.mod`; patterns that match nothing; packages that don't link `google.golang.org/grpc`; and a missing `go` command. A `--config` file is refused when it is missing, empty, white space, cut off at any byte, not JSON, two JSON values, or a JSON value other than an object with at least one key. These checks run before anything is reported, under every `--fail-on` value and both formats.

## Limits

- **Literals and constants only.** A method name built at run time (`"/" + svc + "/" + m`, `fmt.Sprintf`) isn't read, and a service config built at run time, such as one made with `json.Marshal`, is listed in a NOTE as not checked. Pass the JSON with `--config` to check it.
- **Strings, not their use.** grpcgap doesn't know whether a string is compared with `info.FullMethod` or used for something else. A project that names non-gRPC endpoints in the gRPC format gets findings for them; mark those with `//grpcgap:ignore`.
- **Bare method names aren't checked.** A list keyed by `"GetOrder"` alone can't be tied to a service.
- **Load balancing isn't checked.** `loadBalancingPolicy` names aren't compared with the registered balancers (grpc-go falls back to `pick_first` for an unknown name without an error), and `loadBalancingConfig` is listed in a NOTE.
- **Linked services only.** A client that calls another team's service through `ClientConn.Invoke` with a string, without linking its generated code, gets `unknown-service` for that name.
- **Descriptors must be literals.** A `grpc.ServiceDesc` built at run time, as some proxies do, is listed in a NOTE, and names of its methods are reported as unknown.
- **One platform per run.** It reads the files the go command builds for the current `GOOS`, `GOARCH` and `--tags`. Test files aren't read.
- **grpc-go only.** Connect, Twirp and gRPC in other languages aren't covered.

## FAQ

**Why doesn't my grpc-go retry policy work?** The usual causes are a `retryPolicy` that isn't inside a `methodConfig` entry, a `methodConfig` entry without `name`, a `name` whose `service` doesn't match the full proto service name (`package.Service`), snake_case keys, and `grpc.WithDisableRetry()`. grpc-go reports none of these. grpcgap reports all of them and lists which methods each config really reaches.

**What is a gRPC full method name?** `/package.Service/Method`, with the proto package, the service and the method as written in the `.proto` file. It's what `info.FullMethod` holds in an interceptor and what generated code passes to `Invoke`. Generated code also has constants for it, such as `shopv1.Orders_GetOrder_FullMethodName`; using those instead of string literals makes a rename a compile error.

**Does grpcgap run my code or connect to my server?** No. It runs `go list -deps -export`, which compiles packages into the build cache, and parses source files.

**Does it support service configs from DNS or xDS?** Not directly. Save the JSON to a file and pass it with `--config`.

**Is it an MCP server?** No. It's a skill with one Go program, so `go run` or `go build` is all it needs.

## Tested on

- **The real grpc-go at run time.** `tests/live_test.go` builds two oracle programs against the vendored grpc-go v1.70.0. One gets 43 service configs in `tests/testdata/configs`, and the test checks that grpcgap agrees with `grpc.NewClient` on which ones are rejected and with `ClientConn.GetMethodConfig` on the settings every method gets; this found one rule the first version got wrong (retry throttling is only checked when there is a `methodConfig`). The other calls 15 method names on a real server and records which ones reach its interceptors; grpcgap must flag exactly the gRPC-looking ones that don't, and leave 6 path-like strings alone. `TestDemo` runs `cmd/demo` and checks that grpcgap flags exactly the clients that didn't retry and the admin entry that let a user through.
- **Real projects, by hand,** with Go 1.27.3 on linux/arm64:
  - smartcontractkit/chainlink-common `pkg/chipingress` at 137d133, the commit before PR 2373: reported the five retry policy fields at the top level of the service config, at `client.go:104`. At the fix, 83eb230, no finding, and a NOTE that the config is built at run time.
  - temporalio/temporal at a1e0052: 213 method names checked, 10 errors. One is a quota entry for `SetCurrentDeploymentVersion`, which the linked WorkflowService doesn't have; the line is marked `[cleanup-wv-pre-release]`. The other nine are names Temporal uses on purpose for its Nexus and OpenAPI HTTP endpoints and long-poll variants, and its comments say they aren't gRPC methods. Those need `//grpcgap:ignore`.
  - etcd-io/etcd at 1e1a71a (with its `go.work`) and dapr/dapr at 2707e0a: no problems; 2 and 13 method names and one `loadBalancingPolicy` config each. Dapr gets a NOTE for its proxy's run-time `grpc.ServiceDesc`.
  - containerd/containerd at 4ba4c89: nothing checked; its method name strings are all in generated code or its separate `api` module.
- **Input it can't trust:** tests replace `go.mod`, `vendor/modules.txt`, a source file, a client file, the generated `_grpc.pb.go`, a vendored grpc-go file and the vendored health service with empty, garbage, build-log, test-log, `kubectl`-error and HTML content, each with a real `--config` file too; cut source files at every line boundary; cut a `--config` file at every byte; and give the other refusals listed above. Each must exit 2 with nothing on stdout under every `--fail-on` value and both formats. A cut source file is allowed through only when `go build` accepts the cut tree too and the report is unchanged.
- **Exit codes:** with and without `--fail-on`, on errors, on warnings only, and on a healthy program.
- **The installed plugin:** a test copies `plugin/` out of the repository and builds it with the skill's own command.

Not tested: macOS and Windows; grpc-go releases other than v1.70.0 at run time (the parser source was compared with v1.85.0, and the real projects use v1.83.2 and v1.85.0); cgo; a fresh Claude Code session using the skill. The `go run ...@v0.1.0` lines work only once the repository is published.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/grpcgap/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `grpcgap` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)
