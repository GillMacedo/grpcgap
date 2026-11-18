package main

import (
	"net/url"
	"path/filepath"
	"strings"
)

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool struct {
		Driver struct {
			Name           string `json:"name"`
			Version        string `json:"version"`
			InformationURI string `json:"informationUri"`
		} `json:"driver"`
	} `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifResult struct {
	RuleID  string `json:"ruleId"`
	Level   string `json:"level"`
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifLocation struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region *sarifRegion `json:"region,omitempty"`
	} `json:"physicalLocation"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

func toSARIF(rep *report) sarifLog {
	log := sarifLog{
		Schema:  "https://docs.oasis-open.org/sarif/sarif/v2.1.0/os/schemas/sarif-schema-2.1.0.json",
		Version: "2.1.0",
		Runs:    []sarifRun{{Results: make([]sarifResult, 0, len(rep.Findings))}},
	}
	run := &log.Runs[0]
	run.Tool.Driver.Name = "grpcgap"
	run.Tool.Driver.Version = version
	run.Tool.Driver.InformationURI = "https://github.com/GillMacedo/grpcgap"
	for _, f := range rep.Findings {
		result := sarifResult{RuleID: f.Code, Level: "warning"}
		if f.Severity == "ERROR" {
			result.Level = "error"
		}
		result.Message.Text = f.Message
		file, line := splitPlace(f.Place)
		location := sarifLocation{}
		location.PhysicalLocation.ArtifactLocation.URI = sarifFileURI(file)
		if line > 0 {
			location.PhysicalLocation.Region = &sarifRegion{StartLine: line}
		}
		result.Locations = []sarifLocation{location}
		run.Results = append(run.Results, result)
	}
	return log
}

// Artifact locations are URI references, so filenames containing #, ?, or spaces
// need escaping. Absolute paths use file URIs, including Windows drive paths.
func sarifFileURI(file string) string {
	uri := url.URL{Path: filepath.ToSlash(file)}
	if filepath.IsAbs(file) {
		uri.Scheme = "file"
		if !strings.HasPrefix(uri.Path, "/") {
			uri.Path = "/" + uri.Path
		}
	}
	return uri.String()
}
