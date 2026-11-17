# 0001: A skill with one Go program

**Decision:** grpcgap is a Claude Code skill that builds and runs `plugin/scripts`, a single Go `main` package that uses only the standard library.

**Why:** the check is a pure function of the source and config files, so an MCP server would add a process and an npm package for nothing. Claude Code installs only `plugin/`, with no `go.mod`, so the program has to build from its `.go` files alone. `tests/plugin_test.go` builds a copy of `plugin/` outside the module with the skill's own command.

**Alternatives:** an MCP server (rejected as above); a `go/analysis` analyzer for `go vet -vettool` (it would need `golang.org/x/tools`, and the service configs in `--config` files aren't Go code).
