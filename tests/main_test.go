// Package tests runs grpcgap against real Go modules: the example in
// examples/shop, programs in testdata that the tests build and run against
// the vendored grpc-go, and broken copies of both.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

var (
	binary   string
	repoRoot string
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	root, err := filepath.Abs("..")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	repoRoot = root
	tmp, err := os.MkdirTemp("", "grpcgap-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	binary = filepath.Join(tmp, "grpcgap"+exe)
	build := exec.Command("go", "build", "-o", binary, "./plugin/scripts")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building grpcgap: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// goEnv keeps the go command offline and on the toolchain running the
// tests, so a broken go.mod can't make it download anything.
func goEnv() []string {
	return append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local", "GOFLAGS=", "GOWORK=off")
}

type result struct {
	stdout string
	stderr string
	code   int
}

func runTool(t *testing.T, dir string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = goEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running grpcgap: %v", err)
		}
		code = ee.ExitCode()
	}
	return result{stdout.String(), stderr.String(), code}
}

type jsonFinding struct {
	Severity string   `json:"severity"`
	Code     string   `json:"code"`
	Place    string   `json:"place"`
	Value    string   `json:"value"`
	Config   string   `json:"serviceConfig"`
	Programs []string `json:"programs"`
}

type jsonConfig struct {
	Place   string `json:"place"`
	Valid   bool   `json:"valid"`
	Error   string `json:"error"`
	Applies []struct {
		Name     string `json:"name"`
		Settings string `json:"settings"`
	} `json:"applies"`
}

type jsonReport struct {
	Programs []string `json:"programs"`
	Services []struct {
		Name    string   `json:"name"`
		Methods []string `json:"methods"`
	} `json:"services"`
	MethodRefs  int           `json:"methodNamesChecked"`
	HealthNames int           `json:"healthServiceNamesChecked"`
	Configs     []jsonConfig  `json:"serviceConfigs"`
	Notes       []string      `json:"notes"`
	Findings    []jsonFinding `json:"findings"`
}

func runJSON(t *testing.T, dir string, args ...string) jsonReport {
	t.Helper()
	r := runTool(t, dir, append([]string{"--format", "json", "--fail-on", "never"}, args...)...)
	if r.code != 0 {
		t.Fatalf("grpcgap %v in %s: exit %d\n%s", args, dir, r.code, r.stderr)
	}
	var rep jsonReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, r.stdout)
	}
	return rep
}

// copyTree copies a directory, so tests can change files in the copy.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copying %s: %v", src, err)
	}
}

// shopCopy returns a fresh copy of examples/shop.
func shopCopy(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "shop")
	copyTree(t, filepath.Join(repoRoot, "examples", "shop"), dir)
	return dir
}

// runProgram runs a package of a shop copy for real and returns its
// output.
func runProgram(t *testing.T, dir, pkg string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", pkg}, args...)...)
	cmd.Dir = dir
	cmd.Env = goEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
