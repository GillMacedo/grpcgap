package main

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestSARIFLocations(t *testing.T) {
	for _, tc := range []struct {
		place string
		uri   string
		line  int
	}{
		{"src/a b#?.go:12", "src/a%20b%23%3F.go", 12},
		{"file:part.go:8", "./file:part.go", 8},
		{"config.json", "config.json", 0},
	} {
		t.Run(tc.place, func(t *testing.T) {
			log := toSARIF(&report{Findings: []finding{{
				Severity: "WARNING", Code: "retry-capped", Place: tc.place, Message: "capped",
			}}})
			result := log.Runs[0].Results[0]
			location := result.Locations[0].PhysicalLocation
			if result.Level != "warning" || location.ArtifactLocation.URI != tc.uri {
				t.Errorf("unexpected result: %+v", result)
			}
			if tc.line == 0 {
				if location.Region != nil {
					t.Errorf("unknown source line must omit region: %+v", location)
				}
			} else if location.Region == nil || location.Region.StartLine != tc.line {
				t.Errorf("source line: got %+v, want %d", location.Region, tc.line)
			}
		})
	}

	t.Run("absolute filenames use escaped file URIs", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "name #?.json")
		parsed, err := url.Parse(sarifFileURI(file))
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.ToSlash(file)
		if !strings.HasPrefix(want, "/") {
			want = "/" + want
		}
		if parsed.Scheme != "file" || parsed.Path != want || parsed.RawQuery != "" || parsed.Fragment != "" {
			t.Errorf("file URI: got %+v, want file path %q", parsed, want)
		}
	})
}
