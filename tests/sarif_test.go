package tests

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

type sarifLog struct {
	Schema  string `json:"$schema"`
	Version string `json:"version"`
	Runs    []struct {
		Tool struct {
			Driver struct{ Name, Version string }
		}
		Results []struct {
			RuleID    string `json:"ruleId"`
			Level     string
			Message   struct{ Text string }
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct{ URI string }
					Region           struct{ StartLine int }
				}
			}
		}
	}
}

func TestSARIF(t *testing.T) {
	dir := filepath.Join(repoRoot, "examples", "shop")
	jsonResult := runJSON(t, dir)
	result := runTool(t, dir, "--format", "sarif")
	if result.code != 1 || result.stderr != "" {
		t.Fatalf("SARIF: exit %d, stderr %s", result.code, result.stderr)
	}
	var log sarifLog
	if err := json.Unmarshal([]byte(result.stdout), &log); err != nil {
		t.Fatalf("bad SARIF: %v\n%s", err, result.stdout)
	}
	if log.Version != "2.1.0" || log.Schema == "" || len(log.Runs) != 1 {
		t.Fatalf("bad SARIF envelope: %+v", log)
	}
	run := log.Runs[0]
	if run.Tool.Driver.Name != "grpcgap" || run.Tool.Driver.Version == "" || len(run.Results) != 4 {
		t.Fatalf("bad tool or result count: %+v", run)
	}
	if len(run.Results) != len(jsonResult.Findings) {
		t.Fatalf("got %d SARIF results, want %d JSON findings", len(run.Results), len(jsonResult.Findings))
	}
	for i, finding := range jsonResult.Findings {
		got := run.Results[i]
		if len(got.Locations) != 1 {
			t.Fatalf("result %d has %d locations, want 1", i, len(got.Locations))
		}
		if got.RuleID != finding.Code || got.Message.Text != finding.Message || got.Level != "error" {
			t.Errorf("result %d does not describe %+v: %+v", i, finding, got)
		}
		place := got.Locations[0].PhysicalLocation
		uri, err := url.Parse(place.ArtifactLocation.URI)
		if err != nil || uri.Path == "" || place.Region.StartLine < 1 {
			t.Fatalf("invalid source location: %+v (%v)", place, err)
		}
		if got := fmt.Sprintf("%s:%d", uri.Path, place.Region.StartLine); got != finding.Place {
			t.Errorf("location got %s, want %s", got, finding.Place)
		}
	}

	t.Run("healthy output has an empty results array", func(t *testing.T) {
		result := runTool(t, dir, "--format", "sarif", "./cmd/billing")
		if result.code != 0 {
			t.Fatalf("healthy SARIF: exit %d\n%s", result.code, result.stderr)
		}
		var healthy sarifLog
		if err := json.Unmarshal([]byte(result.stdout), &healthy); err != nil {
			t.Fatal(err)
		}
		if len(healthy.Runs) != 1 || healthy.Runs[0].Results == nil || len(healthy.Runs[0].Results) != 0 {
			t.Errorf("results must be [], got %+v", healthy)
		}
	})

	t.Run("refused input has no stdout", func(t *testing.T) {
		result := runTool(t, dir, "--format", "sarif", "--config", filepath.Join(t.TempDir(), "missing.json"))
		if result.code != 2 || result.stdout != "" || result.stderr == "" {
			t.Errorf("refusal: %+v", result)
		}
	})

	for _, failOn := range []struct {
		name string
		code int
	}{{"error", 0}, {"warning", 1}, {"never", 0}} {
		t.Run("warning with fail-on "+failOn.name, func(t *testing.T) {
			config := filepath.Join(repoRoot, "tests", "testdata", "configs", "capped-attempts.json")
			result := runTool(t, dir, "--format", "sarif", "--config", config,
				"--fail-on", failOn.name, "./cmd/billing")
			if result.code != failOn.code {
				t.Fatalf("warning exit got %d, want %d\n%s", result.code, failOn.code, result.stderr)
			}
			var warnings sarifLog
			if err := json.Unmarshal([]byte(result.stdout), &warnings); err != nil {
				t.Fatal(err)
			}
			if len(warnings.Runs) != 1 || len(warnings.Runs[0].Results) != 1 {
				t.Fatalf("expected one warning: %+v", warnings)
			}
			finding := warnings.Runs[0].Results[0]
			if finding.RuleID != "retry-capped" || finding.Level != "warning" {
				t.Errorf("unexpected warning: %+v", finding)
			}
		})
	}
}

func TestSARIFPathsWithDir(t *testing.T) {
	module := filepath.Join(repoRoot, "examples", "shop")
	config := filepath.Join(repoRoot, "tests", "testdata", "configs", "capped-attempts.json")
	outside := t.TempDir()
	// macOS temporary directories may be reached through /var -> /private/var.
	outside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(outside, "configs #")
	externalConfig := filepath.Join(configDir, "capped attempts.json")
	writeFile(t, externalConfig, readFile(t, config))

	for _, tc := range []struct {
		name, cwd, dir, configArg, configFile string
	}{
		{"relative module and config", repoRoot, filepath.Join("examples", "shop"), filepath.Join("tests", "testdata", "configs", "capped-attempts.json"), config},
		{"absolute module and config", repoRoot, module, config, config},
		{"outside the repository", outside, module, config, config},
		{"config directory outside the module", outside, module, "configs #", externalConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runTool(t, tc.cwd, "--dir", tc.dir, "--format", "sarif", "--fail-on", "never",
				"--config", tc.configArg, "./cmd/billing", "./internal/auth", "./internal/reportsclient")
			if result.code != 0 || result.stderr != "" {
				t.Fatalf("exit %d, stderr %s", result.code, result.stderr)
			}
			var log sarifLog
			if err := json.Unmarshal([]byte(result.stdout), &log); err != nil {
				t.Fatal(err)
			}
			if len(log.Runs) != 1 || len(log.Runs[0].Results) == 0 {
				t.Fatalf("missing findings: %+v", log)
			}
			sawGo, sawEmbeddedConfig, sawConfig := false, false, false
			for _, finding := range log.Runs[0].Results {
				if len(finding.Locations) != 1 {
					t.Fatalf("expected one location: %+v", finding)
				}
				location := finding.Locations[0].PhysicalLocation
				uri, err := url.Parse(location.ArtifactLocation.URI)
				if err != nil {
					t.Fatal(err)
				}
				if uri.IsAbs() || uri.Host != "" || uri.RawQuery != "" || uri.Fragment != "" || location.Region.StartLine < 1 {
					t.Fatalf("expected an escaped, relative source location: %+v", location)
				}
				resolved := filepath.Join(tc.cwd, filepath.FromSlash(uri.Path))
				if info, err := os.Stat(resolved); err != nil || info.IsDir() {
					t.Fatalf("URI %q does not resolve to a file from %s: %v", uri, tc.cwd, err)
				}
				want := tc.configFile
				if filepath.Ext(resolved) == ".go" {
					want = filepath.Join(module, "internal", "auth", "auth.go")
					if filepath.Base(resolved) == "client.go" {
						want = filepath.Join(module, "internal", "reportsclient", "client.go")
						sawEmbeddedConfig = true
					}
					sawGo = true
				} else {
					sawConfig = true
				}
				relative, err := filepath.Rel(tc.cwd, want)
				if err != nil {
					t.Fatal(err)
				}
				if uri.Path != filepath.ToSlash(relative) {
					t.Errorf("URI path got %q, want %q", uri.Path, filepath.ToSlash(relative))
				}
			}
			if !sawGo || !sawEmbeddedConfig || !sawConfig {
				t.Errorf("must check Go, embedded config and external config locations: Go=%v embedded=%v external=%v", sawGo, sawEmbeddedConfig, sawConfig)
			}
		})
	}
}
