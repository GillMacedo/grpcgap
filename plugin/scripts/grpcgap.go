// Command grpcgap checks the gRPC method names and service configs in a Go
// program against the gRPC services the program links.
//
// A string such as "/shop.v1.Admin/PurgeCustomer" in an interceptor's
// allow-list, or a methodConfig name in a service config, silently matches
// nothing when the method was renamed, moved to another service or never
// existed. A service config with a misplaced or misspelled field parses
// without error and does nothing. grpcgap runs go list -deps -export, reads
// every grpc.ServiceDesc in the linked packages, and reports the names and
// fields gRPC will never use. It doesn't run the program.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("grpcgap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "module directory to run go list in")
	tags := fs.String("tags", "", "build tags, passed to go list -tags")
	format := fs.String("format", "text", "output format: text or json")
	failOn := fs.String("fail-on", "error", "exit 1 on errors (error), on errors and warnings (warning), or never")
	var configs multiFlag
	fs.Var(&configs, "config", "a service config JSON file to check too (repeatable)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: grpcgap [--dir DIR] [--tags TAGS] [--config FILE]... [--format text|json] [--fail-on error|warning|never] [packages]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "grpcgap", version)
		return 0
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "grpcgap: --format must be text or json, not %q\n", *format)
		return 2
	}
	if *failOn != "error" && *failOn != "warning" && *failOn != "never" {
		fmt.Fprintf(stderr, "grpcgap: --fail-on must be error, warning or never, not %q\n", *failOn)
		return 2
	}
	patterns := fs.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	rep, err := analyze(*dir, *tags, patterns, configs)
	if err != nil {
		fmt.Fprintf(stderr, "grpcgap: can't trust the input, nothing was reported: %v\n", err)
		return 2
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(stderr, "grpcgap: %v\n", err)
			return 2
		}
	} else {
		writeText(stdout, rep)
	}
	errs, warns := counts(rep)
	switch {
	case *failOn == "error" && errs > 0:
		return 1
	case *failOn == "warning" && errs+warns > 0:
		return 1
	}
	return 0
}

func analyze(dir, tags string, patterns, configs []string) (*report, error) {
	if !isDir(dir) {
		return nil, refuse("--dir %s is not a directory", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, refuse("--dir %s: %v", dir, err)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	files, err := readConfigFiles(configs)
	if err != nil {
		return nil, err
	}
	list, err := goList(abs, tags, patterns)
	if err != nil {
		return nil, err
	}
	rep, err := check(abs, list, files)
	if err != nil {
		return nil, err
	}
	rep.Dir = filepath.ToSlash(dir)
	if rep.Programs == nil {
		rep.Programs = []string{}
	}
	if rep.Services == nil {
		rep.Services = []serviceReport{}
	}
	if rep.Configs == nil {
		rep.Configs = []configReport{}
	}
	if rep.NotChecked == nil {
		rep.NotChecked = []unchecked{}
	}
	if rep.Notes == nil {
		rep.Notes = []string{}
	}
	if rep.Findings == nil {
		rep.Findings = []finding{}
	}
	return rep, nil
}

func counts(rep *report) (errs, warns int) {
	for _, f := range rep.Findings {
		if f.Severity == "ERROR" {
			errs++
		} else {
			warns++
		}
	}
	return
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func writeText(w io.Writer, rep *report) {
	methods := 0
	for _, s := range rep.Services {
		methods += len(s.Methods)
	}
	fmt.Fprintf(w, "%s: %s, %s linking %s; %s with %s\n", rep.Tool, rep.Dir,
		plural(len(rep.Programs), "program", "programs"), rep.GRPC,
		plural(len(rep.Services), "service", "services"), plural(methods, "method", "methods"))
	for _, f := range rep.Findings {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s %s  %s  %s\n", f.Severity, f.Code, f.Place, f.Value)
		fmt.Fprintf(w, "  %s\n", f.Message)
		if f.Config != "" && f.Config != f.Place {
			fmt.Fprintf(w, "  in the service config at %s\n", f.Config)
		}
		if f.Programs != nil && len(rep.Programs) > 0 {
			if len(f.Programs) > 0 {
				fmt.Fprintf(w, "  programs: %s\n", strings.Join(f.Programs, " "))
			} else {
				fmt.Fprintf(w, "  programs: none of the programs read link this package\n")
			}
		}
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
	}
	if len(rep.Configs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "What each service config applies:")
		for _, c := range rep.Configs {
			switch {
			case !c.Valid:
				fmt.Fprintf(w, "  %s: nothing, gRPC rejects it\n", c.Place)
			case len(c.Applies) == 0 && len(c.Other) == 0:
				fmt.Fprintf(w, "  %s: nothing\n", c.Place)
			default:
				fmt.Fprintf(w, "  %s:\n", c.Place)
				for _, a := range c.Applies {
					fmt.Fprintf(w, "    %s: %s\n", a.Name, a.Settings)
				}
				if len(c.Applies) == 0 {
					fmt.Fprintf(w, "    method settings: none\n")
				}
				for _, o := range c.Other {
					fmt.Fprintf(w, "    channel: %s\n", o)
				}
			}
		}
	}
	if len(rep.NotChecked) > 0 || len(rep.Notes) > 0 {
		fmt.Fprintln(w)
		for _, u := range rep.NotChecked {
			fmt.Fprintf(w, "NOTE %s: grpc.WithDefaultServiceConfig(%s) is built at run time and isn't checked; pass the JSON with --config\n", u.Place, u.Expr)
		}
		for _, n := range rep.Notes {
			fmt.Fprintf(w, "NOTE %s\n", n)
		}
	}
	fmt.Fprintln(w)
	errs, warns := counts(rep)
	checked := fmt.Sprintf("%s and %s", plural(rep.MethodRefs, "method name", "method names"), plural(len(rep.Configs), "service config", "service configs"))
	switch {
	case rep.MethodRefs == 0 && len(rep.Configs) == 0:
		fmt.Fprintln(w, "Nothing checked: the code read has no gRPC method name strings and no service configs.")
	case errs+warns == 0:
		fmt.Fprintf(w, "No problems: %s checked against the linked services.\n", checked)
	default:
		fmt.Fprintf(w, "%s, %s: %s checked.\n", plural(errs, "error", "errors"), plural(warns, "warning", "warnings"), checked)
	}
}
