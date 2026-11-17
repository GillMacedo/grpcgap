# Security

Report vulnerabilities through [GitHub's private vulnerability reporting](https://github.com/GillMacedo/grpcgap/security/advisories/new), not in a public issue.

`grpcgap` runs `go list -deps -export` in the directory you give it and reads Go source files and the `--config` files you name. `go list -export` compiles packages into the Go build cache and, like `go build`, may download modules that aren't in the module cache or `vendor/`; with cgo it runs the C compiler. grpcgap doesn't run your program, write to your files, or connect anywhere itself. Method names, file paths and config values are printed in findings, so don't share its output if those hold anything private.

A stale entry in an authorization allow-list can be a security problem in your service. grpcgap reports it as a normal finding; treat such findings with the care your service needs. A missed or wrong finding is a normal bug. If `grpcgap` changes a file, runs code from the module, or reports a clean result for code or a config it couldn't read, report it privately. `grpcgap` is a diagnostic aid and doesn't enforce anything.
