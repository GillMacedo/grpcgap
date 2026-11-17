package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	good := map[string]time.Duration{
		`"1s"`:           time.Second,
		`"0.1s"`:         100 * time.Millisecond,
		`".25s"`:         250 * time.Millisecond,
		`"2.s"`:          2 * time.Second,
		`"-1.5s"`:        -1500 * time.Millisecond,
		`"0.000000001s"`: time.Nanosecond,
	}
	for in, want := range good {
		var d Duration
		if err := d.UnmarshalJSON([]byte(in)); err != nil || time.Duration(d) != want {
			t.Errorf("%s: got %v, %v; want %v", in, time.Duration(d), err, want)
		}
	}
	for _, in := range []string{`"2"`, `"200ms"`, `"1m"`, `"s"`, `".s"`, `"1.2.3s"`, `"0.0000000001s"`, `2`, `null`, `"999999999999s"`} {
		var d Duration
		if err := d.UnmarshalJSON([]byte(in)); err == nil {
			t.Errorf("%s: accepted as %v", in, time.Duration(d))
		}
	}
}

func TestCode(t *testing.T) {
	for in, want := range map[string]Code{`"UNAVAILABLE"`: 14, `14`: 14, `0`: 0, `"CANCELLED"`: 1, `null`: 0} {
		var c Code
		if err := c.UnmarshalJSON([]byte(in)); err != nil || c != want {
			t.Errorf("%s: got %v, %v", in, c, err)
		}
	}
	for _, in := range []string{`"unavailable"`, `"Unavailable"`, `17`, `-1`, `14.0`, `"CANCELED"`} {
		var c Code
		if err := c.UnmarshalJSON([]byte(in)); err == nil {
			t.Errorf("%s: accepted as %v", in, c)
		}
	}
}

func TestParseJSON(t *testing.T) {
	n, err := parseJSON([]byte("  {\"a\": [1, {\"b\": null}], \"a\": \"x\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n.kind != 'o' || len(n.members) != 2 || n.members[0].key != "a" || n.members[1].val.str != "x" {
		t.Fatalf("tree: %+v", n)
	}
	if n.off != 2 || n.members[1].off != 26 {
		t.Errorf("offsets: object %d, second key %d", n.off, n.members[1].off)
	}
	if got := string(n.members[1].val.raw); got != `"x"` {
		t.Errorf("raw: %q", got)
	}
	for _, bad := range []string{"", "{", "{} {}", "{} x", `{"a":}`, `{"a" 1}`, "[1,]", "nul"} {
		if _, err := parseJSON([]byte(bad)); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}

func codesOf(m *svcModel) string {
	var c []string
	for _, is := range m.Issues {
		c = append(c, is.Code)
	}
	return strings.Join(c, ",")
}

func TestReadServiceConfig(t *testing.T) {
	retry := `"retryPolicy": {"maxAttempts": 3, "initialBackoff": "0.1s", "maxBackoff": "1s", "backoffMultiplier": 2, "retryableStatusCodes": ["UNAVAILABLE"]}`
	cases := []struct {
		name, json string
		valid      bool
		codes      string
	}{
		{"correct", `{"methodConfig": [{"name": [{"service": "a.B"}], ` + retry + `}]}`, true, ""},
		{"bare", `{"maxAttempts": 3, "initialBackoff": "0.1s"}`, true, "ignored-field"},
		{"top retryPolicy", `{` + retry + `}`, true, "ignored-field"},
		{"snake", `{"method_config": []}`, true, "ignored-field"},
		{"case", `{"METHODCONFIG": [{"Name": [{"Service": "a.B"}], "TIMEOUT": "1s"}]}`, true, ""},
		{"unnamed", `{"methodConfig": [{` + retry + `}]}`, true, "unnamed-method-config"},
		{"empty names", `{"methodConfig": [{"name": []}]}`, true, "empty-name-list"},
		{"dup key", `{"methodConfig": [], "methodConfig": []}`, true, "duplicate-field"},
		{"bad retry", `{"methodConfig": [{"name": [{}], "retryPolicy": {"maxAttempts": 1}}]}`, false, ""},
		{"unnamed bad retry", `{"methodConfig": [{"retryPolicy": {"maxAttempts": 1}}]}`, true, "unnamed-method-config"},
		{"method without service", `{"methodConfig": [{"name": [{"method": "M"}]}]}`, false, ""},
		{"dup name", `{"methodConfig": [{"name": [{}]}, {"name": [{}]}]}`, false, ""},
		{"type error", `{"methodConfig": [{"name": [{"service": 5}]}]}`, false, ""},
		{"empty", ``, false, ""},
		{"throttle alone", `{"retryThrottling": {"maxTokens": 0}}`, true, ""},
		{"throttle checked", `{"retryThrottling": {"maxTokens": 0}, "methodConfig": []}`, false, ""},
	}
	for _, c := range cases {
		m := readServiceConfig(c.json)
		if m.Valid != c.valid || codesOf(m) != c.codes {
			t.Errorf("%s: valid=%v (%s) codes=%q; want valid=%v codes=%q", c.name, m.Valid, m.Err, codesOf(m), c.valid, c.codes)
		}
	}
}

func TestIgnoredFieldsAreGrouped(t *testing.T) {
	m := readServiceConfig(`{"maxAttempts": 3, "initialBackoff": "0.1s", "foo": 1, "bar": 2}`)
	if len(m.Issues) != 3 {
		t.Fatalf("want one issue for the retry fields and one each for foo and bar, got %+v", m.Issues)
	}
	if !strings.Contains(m.Issues[0].Message, `"maxAttempts", "initialBackoff"`) || !strings.Contains(m.Issues[0].Message, "retryPolicy") {
		t.Errorf("message: %s", m.Issues[0].Message)
	}
	m = readServiceConfig(`{"methodConfig": [{"name": [{"service": "a.B"}], "wait_for_ready": true}]}`)
	if len(m.Issues) != 1 || !strings.Contains(m.Issues[0].Fix, `"waitForReady"`) {
		t.Errorf("snake_case fix: %+v", m.Issues)
	}
}

func TestLookup(t *testing.T) {
	m := readServiceConfig(`{"methodConfig": [{"name": [{}], "timeout": "1s"}, {"name": [{"service": "a.B"}], "timeout": "2s"}, {"name": [{"service": "a.B", "method": "C"}], "timeout": "3s"}]}`)
	for full, want := range map[string]string{"/a.B/C": "timeout 3s", "/a.B/D": "timeout 2s", "/x.Y/Z": "timeout 1s"} {
		if got := m.lookup(full).describe(); got != want {
			t.Errorf("%s: %s, want %s", full, got, want)
		}
	}
}

func TestFullMethodRe(t *testing.T) {
	for _, s := range []string{"/a.B/C", "/B/C", "/a.b.v1.Svc/", "/_x.Y/Z_1"} {
		if !fullMethodRe.MatchString(s) {
			t.Errorf("%s: not matched", s)
		}
	}
	for _, s := range []string{"/a/b/c", "a.B/C", "/a.B/C/", "/1a.B/C", "/a.B/C.d", "/", "//"} {
		if fullMethodRe.MatchString(s) {
			t.Errorf("%s: matched", s)
		}
	}
}

func TestClosest(t *testing.T) {
	if got := closest("shop.v1.Order", []string{"shop.v1.Orders", "shop.v1.Admin"}); got != "shop.v1.Orders" {
		t.Errorf("got %q", got)
	}
	if got := closest("PurgeCustomer", []string{"DeleteCustomer", "ExportOrders"}); got != "" {
		t.Errorf("got %q, want no suggestion", got)
	}
}

func TestLineAt(t *testing.T) {
	site := &configSite{Text: "{\n  \"a\": 1,\n  \"b\": 2\n}", Place: "x/y.go:10", Line: 10, Raw: true}
	if got := site.lineAt(strings.Index(site.Text, `"b"`)); got != "x/y.go:12" {
		t.Errorf("raw: %s", got)
	}
	site.Raw = false
	if got := site.lineAt(5); got != "x/y.go:10" {
		t.Errorf("interpreted: %s", got)
	}
	file := &configSite{Text: "{\n\"a\": 1}", Place: "c.json", IsFile: true, Raw: true}
	if got := file.lineAt(2); got != "c.json:2" {
		t.Errorf("file: %s", got)
	}
}

func TestRunFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--version"}, &out, &errb); code != 0 || out.String() != "grpcgap "+version+"\n" {
		t.Errorf("--version: %d %q", code, out.String())
	}
	for _, args := range [][]string{{"--format", "yaml"}, {"--fail-on", "maybe"}, {"--bogus"}} {
		out.Reset()
		if code := run(args, &out, &errb); code != 2 || out.Len() != 0 {
			t.Errorf("%v: exit %d, stdout %q", args, code, out.String())
		}
	}
}
