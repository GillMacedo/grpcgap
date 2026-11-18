package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestDemo runs examples/shop/cmd/demo, which uses the shop's real client
// and server code over an in-memory connection, and checks that grpcgap
// flags exactly the clients that don't retry and the admin entry that lets
// a user through.
func TestDemo(t *testing.T) {
	dir := filepath.Join(repoRoot, "examples", "shop")
	out, err := runProgram(t, dir, "./cmd/demo")
	if err != nil {
		t.Fatalf("demo: %v\n%s", err, out)
	}
	want := []string{
		"checkout: GetOrder while the server restarts: Unavailable after 1 attempt",
		"billing: GetOrder while the server restarts: OK after 3 attempts",
		"reports: GetOrder while the server restarts: Unavailable after 1 attempt",
		"api: DeleteCustomer as role=user: OK",
		"api: ExportOrders as role=user: PermissionDenied",
	}
	if got := strings.TrimSpace(out); got != strings.Join(want, "\n") {
		t.Fatalf("demo output changed:\n%s", got)
	}
	rep := runJSON(t, dir)
	flagged := map[string]bool{}
	for _, f := range rep.Findings {
		flagged[strings.Split(f.Place, ":")[0]] = true
	}
	for file, bad := range map[string]bool{
		"internal/ordersclient/client.go":  true,  // checkout: 1 attempt
		"internal/reportsclient/client.go": true,  // reports: 1 attempt
		"internal/billingclient/client.go": false, // billing: 3 attempts
		"internal/auth/auth.go":            true,  // DeleteCustomer let through
	} {
		if flagged[file] != bad {
			t.Errorf("%s: flagged %v, want %v", file, flagged[file], bad)
		}
	}
	quiet := runJSON(t, dir, "./cmd/billing")
	if len(quiet.Findings) != 0 || len(quiet.Configs) != 1 || quiet.MethodRefs != 1 {
		t.Errorf("cmd/billing should be checked and quiet: %+v", quiet)
	}
}

// expectedCodes lists the finding codes grpcgap must report for each
// config in testdata/configs. Configs not listed must have none.
var expectedCodes = map[string][]string{
	"array-root":                {"invalid-service-config"},
	"bare-retry-policy":         {"ignored-field"},
	"capped-attempts":           {"retry-capped"},
	"code-17":                   {"invalid-service-config"},
	"duplicate-key":             {"duplicate-field"},
	"duplicate-key-fold":        {"duplicate-field"},
	"duplicate-name":            {"invalid-service-config"},
	"empty-name-list":           {"empty-name-list"},
	"empty-object":              {"empty-service-config"},
	"empty-service-with-method": {"invalid-service-config"},
	"float-attempts":            {"invalid-service-config"},
	"invalid-duration":          {"invalid-service-config"},
	"invalid-duration-ms":       {"invalid-service-config"},
	"invalid-retry-attempts":    {"invalid-service-config"},
	"lowercase-code":            {"invalid-service-config"},
	"method-case":               {"unknown-method"},
	"method-config-object":      {"invalid-service-config"},
	"method-wrong-service":      {"unknown-method"},
	"name-null":                 {"unnamed-method-config"},
	"no-name":                   {"ignored-field", "unnamed-method-config"},
	"null-backoff":              {"invalid-service-config"},
	"null-root":                 {"empty-service-config"},
	"retry-in-name":             {"ignored-field"},
	"service-typo":              {"unknown-service"},
	"snake-case":                {"ignored-field"},
	"snake-case-retry":          {"ignored-field"},
	"string-attempts":           {"invalid-service-config"},
	// grpc-go only checks retryThrottling when there is a methodConfig, so
	// maxTokens 0 is accepted here, but nothing retries, so the policy does
	// nothing.
	"throttling-bad":        {"throttling-without-retry"},
	"throttling-good":       {"throttling-without-retry"},
	"throttling-with-retry": nil,
	"trailing-garbage":      {"invalid-service-config"},
	"unnamed-invalid-retry": {"unnamed-method-config"},
}

type oracleConfig struct {
	File    string            `json:"file"`
	Valid   bool              `json:"valid"`
	Error   string            `json:"error"`
	Methods map[string]string `json:"methods"`
}

// TestServiceConfigsMatchGRPC puts every config in testdata/configs into a
// program as a grpc.WithDefaultServiceConfig argument, and compares what
// grpcgap says gRPC does with it against what the vendored grpc-go really
// does: whether grpc.NewClient accepts it, and the method config
// GetMethodConfig returns for every method of the linked services.
func TestServiceConfigsMatchGRPC(t *testing.T) {
	dir := shopCopy(t)
	files, err := filepath.Glob(filepath.Join(repoRoot, "tests", "testdata", "configs", "*.json"))
	if err != nil || len(files) < 30 {
		t.Fatalf("found %d configs: %v", len(files), err)
	}
	sort.Strings(files)
	var src strings.Builder
	src.WriteString("package main\n\nimport (\n\t\"google.golang.org/grpc\"\n\t\"google.golang.org/grpc/credentials/insecure\"\n)\n\nfunc main() {\n\tcreds := grpc.WithTransportCredentials(insecure.NewCredentials())\n")
	line := 10
	lineOf := map[int]string{}
	for _, f := range files {
		text := strings.TrimRight(readFile(t, f), "\n")
		if strings.Contains(text, "`") {
			t.Fatalf("%s has a backquote", f)
		}
		lineOf[line] = f
		fmt.Fprintf(&src, "\tgrpc.NewClient(\"passthrough:///x\", creds, grpc.WithDefaultServiceConfig(`%s`))\n", text)
		line += strings.Count(text, "\n") + 1
	}
	src.WriteString("}\n")
	writeFile(t, filepath.Join(dir, "cmd", "configs", "main.go"), src.String())
	writeFile(t, filepath.Join(dir, "cmd", "oracle", "main.go"), readFile(t, filepath.Join(repoRoot, "tests", "testdata", "oracle", "svcconfig", "main.go")))

	out, err := runProgram(t, dir, "./cmd/oracle", files...)
	if err != nil {
		t.Fatalf("oracle: %v\n%s", err, out)
	}
	real := map[string]oracleConfig{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var o oracleConfig
		if err := json.Unmarshal([]byte(l), &o); err != nil {
			t.Fatalf("oracle line %q: %v", l, err)
		}
		real[o.File] = o
	}

	// cmd/api links the shop's services and the health service.
	rep := runJSON(t, dir, "./cmd/configs", "./cmd/api")
	methods := map[string][]string{}
	for _, s := range rep.Services {
		methods[s.Name] = s.Methods
	}
	byFile := map[string]jsonConfig{}
	for _, c := range rep.Configs {
		if !strings.HasPrefix(c.Place, "cmd/configs/") {
			continue
		}
		var n int
		fmt.Sscanf(c.Place[strings.LastIndex(c.Place, ":")+1:], "%d", &n)
		byFile[lineOf[n]] = c
	}
	codes := map[string][]string{}
	for _, f := range rep.Findings {
		if !strings.HasPrefix(f.Place, "cmd/configs/") {
			continue
		}
		var n int
		fmt.Sscanf(f.Config[strings.LastIndex(f.Config, ":")+1:], "%d", &n)
		codes[lineOf[n]] = append(codes[lineOf[n]], f.Code)
	}
	asks := regexp.MustCompile(` \(asks for \d+\)`)
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		o, ok := real[f]
		if !ok {
			t.Fatalf("%s: no oracle result", name)
		}
		c, ok := byFile[f]
		if !ok {
			t.Errorf("%s: grpcgap didn't read this config", name)
			continue
		}
		if c.Valid != o.Valid {
			t.Errorf("%s: grpcgap valid=%v (%s), grpc-go valid=%v (%s)", name, c.Valid, c.Error, o.Valid, o.Error)
			continue
		}
		got := uniqueSorted(codes[f])
		if want := expectedCodes[name]; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: finding codes %v, want %v", name, got, want)
		}
		for _, code := range got {
			if code != "invalid-service-config" && !o.Valid {
				t.Errorf("%s: %s claims gRPC accepts the config silently, but grpc-go rejects it", name, code)
			}
		}
		if !o.Valid {
			continue
		}
		model := map[string]string{}
		for svc, ms := range methods {
			for _, m := range ms {
				full := "/" + svc + "/" + m
				for _, a := range c.Applies {
					if a.Name == full {
						model[full] = a.Settings
					}
				}
				if _, ok := model[full]; ok {
					continue
				}
				for _, a := range c.Applies {
					if a.Name == "/"+svc+"/*" {
						model[full] = a.Settings
					}
				}
				if _, ok := model[full]; ok {
					continue
				}
				for _, a := range c.Applies {
					if a.Name == "every method" {
						model[full] = a.Settings
					}
				}
			}
		}
		for k, v := range model {
			model[k] = asks.ReplaceAllString(v, "")
			if v == "nothing set" {
				delete(model, k)
			}
		}
		if fmt.Sprint(model) != fmt.Sprint(o.Methods) {
			t.Errorf("%s: method configs differ\ngrpcgap: %v\ngrpc-go: %v", name, model, o.Methods)
		}
	}
}

func uniqueSorted(list []string) []string {
	set := map[string]bool{}
	for _, x := range list {
		set[x] = true
	}
	out := []string{}
	for x := range set {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

// methodCases are strings put in a program's code. The oracle says which
// ones gRPC really dispatches; grpcgap must flag the gRPC-looking ones it
// doesn't, and ignore strings that aren't gRPC names.
var methodCases = []string{
	"/shop.v1.Orders/GetOrder",
	"/shop.v1.Orders/getOrder",
	"/shop.v1.Orders/GetOrders",
	"/shop.v1.Order/GetOrder",
	"/shop.v2.Orders/GetOrder",
	"/shop.v1.Admin/PurgeCustomer",
	"/shop.v1.Admin/ExportOrders",
	"/shop.v1.Orders/ExportOrders",
	"/grpc.health.v1.Health/Check",
	"/grpc.health.v1.Health/Watch",
	"/grpc.health.v1.Health/List",
	"/grpc.health.v1.health/Check",
	"/shop.v1.Admin/",
	"/shop.v1.Admins/",
	"/Orders/GetOrder",
}

// notGRPC are strings that look like paths, not gRPC names.
var notGRPC = []string{"/api/v1/orders", "/healthz", "/static/app.js", "/v1/Orders", "/example.com/path", "/debug/pprof/"}

func TestMethodNamesMatchGRPC(t *testing.T) {
	dir := shopCopy(t)
	var src strings.Builder
	src.WriteString("package main\n\nimport \"fmt\"\n\nvar names = []string{\n")
	for _, s := range append(append([]string{}, methodCases...), notGRPC...) {
		fmt.Fprintf(&src, "\t%q,\n", s)
	}
	src.WriteString("\t\"/shop.v1.Admin/Ignored\", //grpcgap:ignore\n")
	src.WriteString("}\n\n// These name HTTP endpoints, not gRPC methods.\n//\n//grpcgap:ignore\nconst (\n\tblockA = \"/shop.v1.Admin/BlockIgnoredA\"\n\tblockB = \"/shop.v1.Admin/BlockIgnoredB\"\n)\n")
	src.WriteString("\nfunc main() { fmt.Println(names, blockA, blockB) }\n")
	writeFile(t, filepath.Join(dir, "cmd", "names", "main.go"), src.String())
	writeFile(t, filepath.Join(dir, "cmd", "names", "names_test.go"), "package main\n\nvar inTest = \"/shop.v1.Admin/OnlyInATest\"\n")
	writeFile(t, filepath.Join(dir, "cmd", "names", "gen.go"), "// Code generated by hand for a test. DO NOT EDIT.\n\npackage main\n\nvar generated = \"/shop.v1.Admin/Generated\"\n")
	writeFile(t, filepath.Join(dir, "cmd", "oracle", "main.go"), readFile(t, filepath.Join(repoRoot, "tests", "testdata", "oracle", "methods", "main.go")))
	out, err := runProgram(t, dir, "./cmd/oracle", methodCases...)
	if err != nil {
		t.Fatalf("oracle: %v\n%s", err, out)
	}
	var seen map[string]bool
	if err := json.Unmarshal([]byte(out), &seen); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}
	// cmd/api links the shop's services and the health service.
	base := runJSON(t, dir, "./cmd/api")
	rep := runJSON(t, dir, "./cmd/names", "./cmd/api")
	flagged := map[string]bool{}
	for _, f := range rep.Findings {
		if !strings.HasPrefix(f.Place, "cmd/names/") {
			continue
		}
		var v string
		json.Unmarshal([]byte(f.Value), &v)
		flagged[v] = true
	}
	for _, s := range methodCases {
		if s == "/Orders/GetOrder" {
			// No package and no linked service of that name: grpcgap
			// can't tell it from a URL path, so it is left alone.
			if flagged[s] || seen[s] {
				t.Errorf("%s: flagged %v, seen by gRPC %v", s, flagged[s], seen[s])
			}
			continue
		}
		if flagged[s] == seen[s] {
			t.Errorf("%s: grpcgap flagged=%v, but gRPC dispatched it=%v", s, flagged[s], seen[s])
		}
	}
	for _, s := range notGRPC {
		if flagged[s] {
			t.Errorf("%s is not a gRPC name but was flagged", s)
		}
	}
	for _, s := range []string{"/shop.v1.Admin/Ignored", "/shop.v1.Admin/BlockIgnoredA", "/shop.v1.Admin/BlockIgnoredB", "/shop.v1.Admin/OnlyInATest", "/shop.v1.Admin/Generated"} {
		if flagged[s] {
			t.Errorf("%s should not be read", s)
		}
	}
	if want := len(methodCases) - 1; rep.MethodRefs-base.MethodRefs != want {
		t.Errorf("checked %d method names, want %d", rep.MethodRefs-base.MethodRefs, want)
	}
}

func TestMain_versionFlag(t *testing.T) {
	r := runTool(t, os.TempDir(), "--version")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "grpcgap 0.1.0" {
		t.Fatalf("--version: %+v", r)
	}
}
