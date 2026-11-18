# Roadmap

## 0.1.0 (this release)
- Check `"/package.Service/Method"` strings in a Go program's own code against the services it links.
- Read `grpc.WithDefaultServiceConfig` arguments, service config string literals and `--config` files the way grpc-go does, and report names that match nothing, fields it ignores, configs it rejects, and retry policies it caps or disables.

## Next
- Check `loadBalancingPolicy` and `loadBalancingConfig` names against the balancers the program registers.
- Read method names built with `fmt.Sprintf` or `+` from a service descriptor constant and a literal method.
- Check health service names passed to `health.Server.SetServingStatus` and Kubernetes `grpc` probes.
- Connect (`connectrpc.com/connect`) procedure names.
- Method name strings in YAML and JSON policy files, with `--strings`.

## Not planned
- Running the program or connecting to a server.
- Changing source files or configs. grpcgap names the change and leaves it to you.
