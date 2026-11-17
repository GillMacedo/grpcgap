package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real output of failed commands, kept to check that a log pasted over a
// source file, go.mod or a service config file is refused.
const (
	failedBuild = `# example.com/shop/internal/auth
internal/auth/auth.go:23:2: syntax error: unexpected newline in composite literal; possibly missing comma or }
`
	failedTest = `--- FAIL: TestConfig (0.00s)
    config_test.go:12: unexpected status
FAIL
FAIL	example.com/shop/internal/ordersclient	0.004s
FAIL
`
	failedKubectl = `Error from server (NotFound): configmaps "grpc-service-config" not found
`
	htmlError = "<html><body><h1>502 Bad Gateway</h1></body></html>\n"
	garbage   = "\x00\x01 this is not Go or JSON \xff\xfe\n{{{"
)

// refused checks the contract for input grpcgap can't trust: exit 2,
// nothing on stdout, and a reason on stderr, under every --fail-on value
// and both formats.
func refused(t *testing.T, dir string, args ...string) {
	t.Helper()
	for _, extra := range [][]string{
		nil,
		{"--fail-on", "never"},
		{"--fail-on", "warning"},
		{"--format", "json"},
		{"--format", "json", "--fail-on", "never"},
	} {
		r := runTool(t, dir, append(append([]string{}, extra...), args...)...)
		if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "can't trust the input") {
			t.Fatalf("args %v: want exit 2, empty stdout and a reason; got exit %d\nstdout: %s\nstderr: %s", append(extra, args...), r.code, r.stdout, r.stderr)
		}
	}
}

var badContents = map[string]string{
	"empty":          "",
	"garbage":        garbage,
	"failed build":   failedBuild,
	"failed test":    failedTest,
	"failed kubectl": failedKubectl,
	"html error":     htmlError,
}

// TestBadTrees replaces one file of the example at a time.
func TestBadTrees(t *testing.T) {
	files := map[string]string{
		"go.mod":          "go.mod",
		"modules.txt":     "vendor/modules.txt",
		"auth":            "internal/auth/auth.go",
		"client":          "internal/ordersclient/client.go",
		"generated":       "gen/shop/v1/orders_grpc.pb.go",
		"vendored grpc":   "vendor/google.golang.org/grpc/server.go",
		"vendored health": "vendor/google.golang.org/grpc/health/grpc_health_v1/health_grpc.pb.go",
	}
	for fname, rel := range files {
		for cname, content := range badContents {
			fname, rel, cname, content := fname, rel, cname, content
			t.Run(fname+"/"+cname, func(t *testing.T) {
				t.Parallel()
				dir := shopCopy(t)
				writeFile(t, filepath.Join(dir, rel), content)
				refused(t, dir)
				// The other input, a service config file, is real.
				cfg := filepath.Join(repoRoot, "tests", "testdata", "configs", "correct.json")
				refused(t, dir, "--config", cfg)
			})
		}
	}
}

// TestBadConfigFiles gives a real program and a --config file that can't
// be trusted.
func TestBadConfigFiles(t *testing.T) {
	dir := filepath.Join(repoRoot, "examples", "shop")
	correct := readFile(t, filepath.Join(repoRoot, "tests", "testdata", "configs", "correct.json"))
	cases := map[string]string{
		"white space":  " \n\t\n",
		"null":         "null\n",
		"array":        "[]\n",
		"string":       "\"methodConfig\"\n",
		"number":       "5\n",
		"empty object": "{}\n",
		"two values":   correct + correct,
	}
	for k, v := range badContents {
		cases[k] = v
	}
	// A config cut off at every byte.
	for i := 1; i < len(strings.TrimRight(correct, "\n")); i++ {
		cases[fmt.Sprintf("cut at %04d", i)] = correct[:i]
	}
	tmp := t.TempDir()
	for name, content := range cases {
		path := filepath.Join(tmp, strings.ReplaceAll(name, " ", "-")+".json")
		writeFile(t, path, content)
		t.Run(name, func(t *testing.T) {
			refused(t, dir, "--config", path, "./cmd/billing")
		})
	}
	t.Run("missing", func(t *testing.T) {
		refused(t, dir, "--config", filepath.Join(tmp, "does-not-exist.json"))
	})
	t.Run("directory", func(t *testing.T) {
		refused(t, dir, "--config", tmp)
	})
}

// TestCutFiles cuts files of the example at line boundaries. A cut file
// must be refused, unless go build accepts the cut tree too and the report
// doesn't change.
func TestCutFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	full := runTool(t, filepath.Join(repoRoot, "examples", "shop"), "--format", "json", "--fail-on", "never")
	if full.code != 0 {
		t.Fatalf("full example: exit %d\n%s", full.code, full.stderr)
	}
	for _, c := range []struct {
		rel  string
		step int
	}{
		{"internal/auth/auth.go", 1},
		{"internal/ordersclient/client.go", 1},
		{"internal/reportsclient/client.go", 1},
		{"gen/shop/v1/orders_grpc.pb.go", 9},
		{"vendor/modules.txt", 5},
		{"go.mod", 1},
	} {
		c := c
		t.Run(c.rel, func(t *testing.T) {
			t.Parallel()
			dir := shopCopy(t)
			path := filepath.Join(dir, c.rel)
			orig := readFile(t, path)
			lines := strings.SplitAfter(orig, "\n")
			for n := 0; n < len(lines); n += c.step {
				cut := strings.Join(lines[:n], "")
				if cut == orig {
					continue
				}
				writeFile(t, path, cut)
				r := runTool(t, dir, "--format", "json", "--fail-on", "never")
				if r.code == 2 && r.stdout == "" {
					continue
				}
				build := exec.Command("go", "build", "./...")
				build.Dir = dir
				build.Env = goEnv()
				if out, err := build.CombinedOutput(); err != nil {
					t.Fatalf("cut after line %d: go build fails but grpcgap exited %d:\n%s\n%s", n, r.code, out, r.stdout)
				}
				if r.stdout != full.stdout {
					t.Fatalf("cut after line %d: go build accepts the cut tree, and the report changed:\n%s", n, r.stdout)
				}
			}
			writeFile(t, path, orig)
		})
	}
}

// TestBadSetups covers refusals that aren't about one file's content.
func TestBadSetups(t *testing.T) {
	shop := filepath.Join(repoRoot, "examples", "shop")
	t.Run("missing dir", func(t *testing.T) {
		refused(t, shop, "--dir", filepath.Join(t.TempDir(), "nope"))
	})
	t.Run("no go.mod", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
		refused(t, dir)
	})
	t.Run("patterns match nothing", func(t *testing.T) {
		refused(t, shop, "./cmd/nothing-here")
	})
	t.Run("no grpc", func(t *testing.T) {
		dir := shopCopy(t)
		writeFile(t, filepath.Join(dir, "cmd", "plain", "main.go"), "package main\n\nvar m = \"/shop.v1.Admin/PurgeCustomer\"\n\nfunc main() { println(m) }\n")
		refused(t, dir, "./cmd/plain")
	})
	t.Run("type error", func(t *testing.T) {
		dir := shopCopy(t)
		p := filepath.Join(dir, "internal", "auth", "auth.go")
		writeFile(t, p, readFile(t, p)+"var _ int = \"x\"\n")
		refused(t, dir)
	})
	t.Run("missing import", func(t *testing.T) {
		dir := shopCopy(t)
		p := filepath.Join(dir, "internal", "auth", "auth.go")
		writeFile(t, p, strings.Replace(readFile(t, p), "import (", "import (\n\t\"example.com/shop/internal/gone\"", 1)+"\nvar _ = gone.X\n")
		refused(t, dir)
	})
	t.Run("vendor removed", func(t *testing.T) {
		dir := shopCopy(t)
		if err := os.RemoveAll(filepath.Join(dir, "vendor")); err != nil {
			t.Fatal(err)
		}
		// GOPROXY=off and GOFLAGS= keep the go command from downloading.
		cmd := exec.Command(binary, "--dir", dir)
		cmd.Env = append(goEnv(), "GOMODCACHE="+t.TempDir())
		out, err := cmd.Output()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 2 || len(out) != 0 {
			t.Fatalf("want exit 2 and no stdout, got %v\n%s", err, out)
		}
	})
	t.Run("no go command", func(t *testing.T) {
		cmd := exec.Command(binary, "--dir", shop)
		cmd.Env = append(goEnv(), "PATH="+t.TempDir())
		out, err := cmd.Output()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 2 || len(out) != 0 {
			t.Fatalf("want exit 2 and no stdout, got %v\n%s", err, out)
		}
	})
	t.Run("bad flags", func(t *testing.T) {
		for _, args := range [][]string{{"--fail-on", "sometimes"}, {"--format", "xml"}, {"--nope"}} {
			r := runTool(t, shop, args...)
			if r.code != 2 || r.stdout != "" {
				t.Errorf("%v: exit %d, stdout %q", args, r.code, r.stdout)
			}
		}
	})
}

// TestExitCodes checks the exit code with and without --fail-on, on
// findings, on warnings only and on a healthy program.
func TestExitCodes(t *testing.T) {
	shop := filepath.Join(repoRoot, "examples", "shop")
	capped := filepath.Join(repoRoot, "tests", "testdata", "configs", "capped-attempts.json")
	cases := []struct {
		name string
		args []string
		want map[string]int // --fail-on value ("" for none) to exit code
	}{
		{"errors", nil, map[string]int{"": 1, "error": 1, "warning": 1, "never": 0}},
		{"warning only", []string{"--config", capped, "./cmd/billing"}, map[string]int{"": 0, "error": 0, "warning": 1, "never": 0}},
		{"healthy", []string{"./cmd/billing"}, map[string]int{"": 0, "error": 0, "warning": 0, "never": 0}},
	}
	for _, c := range cases {
		for failOn, want := range c.want {
			for _, format := range []string{"text", "json"} {
				args := []string{"--format", format}
				if failOn != "" {
					args = append(args, "--fail-on", failOn)
				}
				r := runTool(t, shop, append(args, c.args...)...)
				if r.code != want {
					t.Errorf("%s %v: exit %d, want %d\n%s%s", c.name, args, r.code, want, r.stdout, r.stderr)
				}
				if format == "text" && c.name == "healthy" && !strings.Contains(r.stdout, "No problems: 1 method name and 1 service config checked") {
					t.Errorf("healthy run says:\n%s", r.stdout)
				}
				if format == "text" && c.name != "healthy" && strings.Contains(r.stdout, "No problems") {
					t.Errorf("%s run says No problems:\n%s", c.name, r.stdout)
				}
			}
		}
	}
}
