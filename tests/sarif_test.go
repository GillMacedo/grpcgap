package tests

import (
	"encoding/json"
	"fmt"
	"net/url"
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
	for i, finding := range jsonResult.Findings {
		got := run.Results[i]
		if got.RuleID != finding.Code || got.Message.Text != finding.Message || got.Level != "error" || len(got.Locations) != 1 {
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
