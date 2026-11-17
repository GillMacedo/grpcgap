// Command svcconfig is a test oracle. It is copied into examples/shop at
// test time and built against the vendored grpc-go. For each service
// config file named on the command line it prints, as one JSON line,
// whether grpc.NewClient accepts the config, and the method config gRPC
// really applies to every method of the services the shop links.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	shopv1 "example.com/shop/gen/shop/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

type result struct {
	File    string            `json:"file"`
	Valid   bool              `json:"valid"`
	Error   string            `json:"error,omitempty"`
	Methods map[string]string `json:"methods"`
}

func methods() []string {
	var out []string
	for _, sd := range []grpc.ServiceDesc{shopv1.Orders_ServiceDesc, shopv1.Admin_ServiceDesc, healthpb.Health_ServiceDesc} {
		for _, m := range sd.Methods {
			out = append(out, "/"+sd.ServiceName+"/"+m.MethodName)
		}
		for _, s := range sd.Streams {
			out = append(out, "/"+sd.ServiceName+"/"+s.StreamName)
		}
	}
	sort.Strings(out)
	return out
}

// codeNames are the names gRPC's service config JSON uses for status
// codes, indexed by code.
var codeNames = []string{"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED"}

func describe(mc grpc.MethodConfig) string {
	var parts []string
	if rp := mc.RetryPolicy; rp != nil {
		var codes []string
		for c := range rp.RetryableStatusCodes {
			codes = append(codes, codeNames[c])
		}
		sort.Strings(codes)
		parts = append(parts, fmt.Sprintf("retry up to %d attempts on %s", rp.MaxAttempts, strings.Join(codes, ", ")))
	}
	if mc.Timeout != nil {
		parts = append(parts, "timeout "+mc.Timeout.String())
	}
	if mc.WaitForReady != nil {
		parts = append(parts, fmt.Sprintf("waitForReady %v", *mc.WaitForReady))
	}
	if mc.MaxReqSize != nil {
		parts = append(parts, fmt.Sprintf("max request %d bytes", *mc.MaxReqSize))
	}
	if mc.MaxRespSize != nil {
		parts = append(parts, fmt.Sprintf("max response %d bytes", *mc.MaxRespSize))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func main() {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go srv.Serve(lis)
	defer srv.Stop()
	dialer := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
	enc := json.NewEncoder(os.Stdout)
	for _, file := range os.Args[1:] {
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		r := result{File: file, Methods: map[string]string{}}
		conn, err := grpc.NewClient("passthrough:///oracle", grpc.WithTransportCredentials(insecure.NewCredentials()), dialer, grpc.WithDefaultServiceConfig(string(data)))
		if err != nil {
			r.Error = err.Error()
			enc.Encode(r)
			continue
		}
		r.Valid = true
		// One call makes the channel leave idle mode and apply the
		// default service config. Its result doesn't matter.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
		cancel()
		for _, m := range methods() {
			if d := describe(conn.GetMethodConfig(m)); d != "" {
				r.Methods[m] = d
			}
		}
		conn.Close()
		enc.Encode(r)
	}
}
