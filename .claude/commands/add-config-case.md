Teach grpcgap about a service config case: $ARGUMENTS

1. Add the smallest config that shows it as `tests/testdata/configs/<name>.json`. It must not contain a backquote.
2. Run `go test ./tests -run TestServiceConfigsMatchGRPC`. The test builds `tests/testdata/oracle/svcconfig` against the vendored grpc-go and fails if grpcgap disagrees with grpc-go about whether the config is valid or what each method gets.
3. If the case should be reported, add its finding codes to `expectedCodes` in `tests/live_test.go`; if it is healthy, leave it out so it must stay quiet.
4. Change `plugin/scripts/svcconfig.go` until the test passes, update the README's rule table and Limits, then run `make test`.
