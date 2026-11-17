# grpcgap

Claude Code skill and Go CLI that checks the gRPC method name strings (`"/package.Service/Method"`) and service configs in a Go program against the `grpc.ServiceDesc` of every service the program links, and reports names that match nothing and service config fields grpc-go ignores.

## Architecture
`plugin/scripts/grpcgap.go` holds the CLI and the text report. `plugin/scripts/load.go` runs `go list -deps -export -json` and decodes it strictly (`goList`, `decodeList`). `plugin/scripts/scan.go` parses packages, reads every `grpc.ServiceDesc` composite literal (`findServices`), and collects method name strings and service configs from the main module's own code (`scanOwn`, `addDirect`, `constValue`). `plugin/scripts/svcconfig.go` reads a service config the way grpc-go's `service_config.go` does (`readServiceConfig`, the `json*` mirror types, `Duration`, `Code`) and records ignored fields (`walkIgnored`, `groupIgnored`). `plugin/scripts/jsontree.go` parses JSON with byte offsets so findings name lines. `plugin/scripts/check.go` turns all of it into findings (`check`, `reference`, `config`). `plugin/skills/checking-grpc-names-and-service-configs/SKILL.md` tells Claude how to use it. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet and tests, including `tests/`, which run the example and two oracle programs against the vendored grpc-go and compare
- `make lint`: plugin manifest validation (needs Claude Code)
- `make demo`: run the example's demo and grpcgap on `examples/shop`

## Conventions
- Standard library only. Never run the user's program or change their files; `go list -deps -export` is the only command run.
- Any input it can't trust exits 2 with nothing on stdout, whatever `--fail-on` says. Add a case to `TestBadTrees`, `TestBadConfigFiles` or `TestBadSetups` in `tests/inputs_test.go` for each new kind of input.
- Service config behavior must match grpc-go. A rule change needs a config in `tests/testdata/configs`; `TestServiceConfigsMatchGRPC` compares grpcgap with the real grpc-go for every one.
- A method name rule change needs a string in `methodCases` or `notGRPC` in `tests/live_test.go`; `TestMethodNamesMatchGRPC` calls each one on a real server.
- Tests must not assume the machine they run on: use `runtime` or `go env` for anything platform-specific.

## Do and don't
- Do run `make test` on Go 1.22 and on the current Go release before calling a change done. `GOTOOLCHAIN=go1.22.12 make test` works.
- Don't add `go.mod` requirements to the root module: the installed plugin has no module.
- Don't print "No problems" for a run that checked nothing.
- Don't change `examples/shop` without updating the README output; `tests/readme_test.go` compares them.

## Definition of done
`make test` and `make lint` pass, README output matches a real run, and `ROADMAP.md` and docs are updated when relevant.
