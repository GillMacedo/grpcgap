package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The types below have the same shape and field names as the ones
// grpc-go's service_config.go decodes a service config into (checked
// against grpc-go v1.70.0 and v1.85.0). encoding/json matches keys to
// field names case-insensitively, ignores keys it has no field for, and
// treats null and duplicate keys in its own ways, so decoding into the same
// shapes with the same package reproduces what gRPC sees.

type jsonName struct {
	Service string
	Method  string
}

type jsonRetryPolicy struct {
	MaxAttempts          int
	InitialBackoff       Duration
	MaxBackoff           Duration
	BackoffMultiplier    float64
	RetryableStatusCodes []Code
}

type jsonMC struct {
	Name                    *[]jsonName
	WaitForReady            *bool
	Timeout                 *Duration
	MaxRequestMessageBytes  *int64
	MaxResponseMessageBytes *int64
	RetryPolicy             *jsonRetryPolicy
}

type retryThrottlingPolicy struct {
	MaxTokens  float64
	TokenRatio float64
}

type healthCheckConfig struct {
	ServiceName string
}

type jsonSC struct {
	LoadBalancingPolicy *string
	LoadBalancingConfig *json.RawMessage
	MethodConfig        *[]jsonMC
	RetryThrottling     *retryThrottlingPolicy
	HealthCheckConfig   *healthCheckConfig
}

// defaultMaxCallAttempts is grpc-go's cap on retry attempts when the
// client doesn't set grpc.WithMaxCallAttempts.
const defaultMaxCallAttempts = 5

// Duration reads the protobuf JSON form of a duration, "1.5s", with the
// same rules as grpc-go's internal/serviceconfig.Duration.
type Duration int64

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if !strings.HasSuffix(s, "s") {
		return fmt.Errorf("malformed duration %q: missing seconds unit", s)
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	parts := strings.SplitN(s[:len(s)-1], ".", 3)
	if len(parts) > 2 {
		return fmt.Errorf("malformed duration %q: too many decimals", s)
	}
	digits := false
	var sec, ns int64
	if parts[0] != "" {
		var err error
		if sec, err = strconv.ParseInt(parts[0], 10, 64); err != nil {
			return fmt.Errorf("malformed duration %q: %v", s, err)
		}
		if sec > 315_576_000_000 {
			return fmt.Errorf("out of range: %q", s)
		}
		digits = true
	}
	if len(parts) == 2 && parts[1] != "" {
		if len(parts[1]) > 9 {
			return fmt.Errorf("malformed duration %q: too many digits after decimal", s)
		}
		var err error
		if ns, err = strconv.ParseInt(parts[1], 10, 64); err != nil {
			return fmt.Errorf("malformed duration %q: %v", s, err)
		}
		for i := 9; i > len(parts[1]); i-- {
			ns *= 10
		}
		digits = true
	}
	if !digits {
		return fmt.Errorf("malformed duration %q: contains no numbers", s)
	}
	if neg {
		sec, ns = -sec, -ns
	}
	const maxSec = math.MaxInt64 / int64(time.Second)
	const maxNs = math.MaxInt64 % int64(time.Second)
	const minSec = math.MinInt64 / int64(time.Second)
	const minNs = math.MinInt64 % int64(time.Second)
	switch {
	case sec > maxSec || (sec == maxSec && ns >= maxNs):
		*d = Duration(math.MaxInt64)
	case sec < minSec || (sec == minSec && ns <= minNs):
		*d = Duration(math.MinInt64)
	default:
		*d = Duration(sec*int64(time.Second) + ns)
	}
	return nil
}

// Code reads a status code the way grpc-go's codes.Code does: a number
// below 17, or the exact upper-case name in quotes.
type Code uint32

var codeNames = []string{"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED"}

func (c *Code) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if n, err := strconv.ParseUint(string(b), 10, 32); err == nil {
		if n >= uint64(len(codeNames)) {
			return fmt.Errorf("invalid code: %d", n)
		}
		*c = Code(n)
		return nil
	}
	for i, name := range codeNames {
		if string(b) == `"`+name+`"` {
			*c = Code(i)
			return nil
		}
	}
	return fmt.Errorf("invalid code: %q", string(b))
}

func (c Code) String() string {
	if int(c) < len(codeNames) {
		return codeNames[c]
	}
	return strconv.Itoa(int(c))
}

// methodCfg is what gRPC applies to the calls one name matches.
type methodCfg struct {
	Index        int `json:"methodConfig"`
	WaitForReady *bool
	Timeout      *Duration
	MaxReq       *int64
	MaxResp      *int64
	Retry        *retryCfg
}

type retryCfg struct {
	Requested int
	Attempts  int
	Codes     []string
}

// nameEntry is one methodConfig[i].name[j] entry, with where it is.
type nameEntry struct {
	Service, Method string
	Path            string
	Off             int
	MC              int
}

// cfgIssue is a problem found in one service config, at a byte offset in
// its JSON.
type cfgIssue struct {
	Severity string
	Code     string
	Off      int
	Value    string
	Message  string
	Fix      string
	keys     []string
	group    string
	render   func(keys []string) (string, string)
}

// svcModel is the result of reading one service config like gRPC does.
type svcModel struct {
	Valid       bool
	Err         string
	ErrOff      int
	Methods     map[string]*methodCfg
	Names       []nameEntry
	Issues      []cfgIssue
	LBConfig    bool
	ThrottleOff int      // offset of the retryThrottling key, when it is set
	Retries     bool     // some methodConfig entry has a retry policy
	Sets        bool     // sets anything gRPC uses
	MCOffs      []int    // offset of each methodConfig entry
	Other       []string // settings that aren't per method
	MCs         []*methodCfg
}

// readServiceConfig reads a service config the way grpc-go's
// parseServiceConfig does, and records what it ignores. tree is nil when
// the text isn't valid JSON.
func readServiceConfig(text string) *svcModel {
	m := &svcModel{Methods: map[string]*methodCfg{}}
	if text == "" {
		m.Err = "no JSON service config provided"
		return m
	}
	tree, treeErr := parseJSON([]byte(text))
	if treeErr != nil {
		m.Err = treeErr.Error()
		return m
	}
	walkIgnored(tree, scSchema, "", m)
	m.Issues = groupIgnored(m.Issues)
	var rsc jsonSC
	if err := json.Unmarshal([]byte(text), &rsc); err != nil {
		m.Err = err.Error()
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			m.ErrOff = int(te.Offset)
		}
		return m
	}
	if rsc.LoadBalancingPolicy != nil {
		m.Other = append(m.Other, fmt.Sprintf("loadBalancingPolicy %q", *rsc.LoadBalancingPolicy))
	}
	if rsc.LoadBalancingConfig != nil {
		m.LBConfig = true
		m.Sets = true
		m.Other = append(m.Other, "loadBalancingConfig (not checked)")
	}
	if t := rsc.RetryThrottling; t != nil {
		m.Other = append(m.Other, fmt.Sprintf("retryThrottling maxTokens %v, tokenRatio %v", t.MaxTokens, t.TokenRatio))
		if _, off := memberValue(tree, "RetryThrottling"); off > 0 {
			m.ThrottleOff = off
		}
	}
	if h := rsc.HealthCheckConfig; h != nil {
		m.Other = append(m.Other, fmt.Sprintf("healthCheckConfig serviceName %q", h.ServiceName))
	}
	if rsc.LoadBalancingPolicy != nil || rsc.RetryThrottling != nil || rsc.HealthCheckConfig != nil {
		m.Sets = true
	}
	mcNodes := memberArray(tree, "MethodConfig")
	if rsc.MethodConfig != nil {
		paths := map[string]bool{}
		for i, mc := range *rsc.MethodConfig {
			mcOff := tree.off
			if i < len(mcNodes) {
				mcOff = mcNodes[i].off
			}
			m.MCOffs = append(m.MCOffs, mcOff)
			m.MCs = append(m.MCs, nil)
			if mc.Name == nil {
				m.Issues = append(m.Issues, cfgIssue{
					Severity: "ERROR", Code: "unnamed-method-config", Off: mcOff, Value: fmt.Sprintf("methodConfig[%d]", i),
					Message: "this methodConfig entry has no name, so gRPC skips it" + mcSkips(mc),
					Fix:     `add "name": [{"service": "package.Service"}] for every method of a service, or [{}] for every method of every service`,
				})
				continue
			}
			cfg := &methodCfg{Index: i, WaitForReady: mc.WaitForReady, Timeout: mc.Timeout, MaxReq: mc.MaxRequestMessageBytes, MaxResp: mc.MaxResponseMessageBytes}
			m.MCs[i] = cfg
			if rp := mc.RetryPolicy; rp != nil {
				if !(rp.MaxAttempts > 1 && rp.InitialBackoff > 0 && rp.MaxBackoff > 0 && rp.BackoffMultiplier > 0 && len(rp.RetryableStatusCodes) > 0) {
					m.Err = fmt.Sprintf("invalid retry policy in methodConfig[%d]: maxAttempts must be above 1, initialBackoff, maxBackoff and backoffMultiplier above 0, and retryableStatusCodes not empty", i)
					m.ErrOff = mcOff
					return m
				}
				r := &retryCfg{Requested: rp.MaxAttempts, Attempts: rp.MaxAttempts}
				if r.Attempts > defaultMaxCallAttempts {
					r.Attempts = defaultMaxCallAttempts
				}
				seen := map[Code]bool{}
				for _, c := range rp.RetryableStatusCodes {
					if !seen[c] {
						seen[c] = true
						r.Codes = append(r.Codes, c.String())
					}
				}
				sort.Strings(r.Codes)
				cfg.Retry = r
				m.Retries = true
			}
			nameNodes := []*jnode{}
			if i < len(mcNodes) {
				nameNodes = memberArray(mcNodes[i], "Name")
			}
			if len(*mc.Name) == 0 {
				m.Issues = append(m.Issues, cfgIssue{
					Severity: "ERROR", Code: "empty-name-list", Off: mcOff, Value: fmt.Sprintf("methodConfig[%d].name", i),
					Message: "this methodConfig entry's name list is empty, so it applies to no method" + mcSkips(mc),
					Fix:     `list the services or methods it is for, or use [{}] for every method`,
				})
			}
			for j, n := range *mc.Name {
				off := mcOff
				if j < len(nameNodes) {
					off = nameNodes[j].off
				}
				if n.Service == "" && n.Method != "" {
					m.Err = fmt.Sprintf("methodConfig[%d].name[%d]: cannot combine empty 'service' and non-empty 'method'", i, j)
					m.ErrOff = off
					return m
				}
				path := ""
				if n.Service != "" {
					path = "/" + n.Service + "/" + n.Method
				}
				if paths[path] {
					m.Err = fmt.Sprintf("methodConfig[%d].name[%d]: duplicated name %s", i, j, describePath(path))
					m.ErrOff = off
					return m
				}
				paths[path] = true
				m.Methods[path] = cfg
				m.Names = append(m.Names, nameEntry{Service: n.Service, Method: n.Method, Path: path, Off: off, MC: i})
				m.Sets = true
			}
		}
	}
	// grpc-go returns before checking retryThrottling when there is no
	// methodConfig, so an out-of-range throttling policy is accepted then.
	if t := rsc.RetryThrottling; t != nil && rsc.MethodConfig != nil {
		if t.MaxTokens <= 0 || t.MaxTokens > 1000 {
			m.Err = fmt.Sprintf("invalid retry throttling config: maxTokens (%v) out of range (0, 1000]", t.MaxTokens)
			return m
		}
		if t.TokenRatio <= 0 {
			m.Err = fmt.Sprintf("invalid retry throttling config: tokenRatio (%v) may not be negative", t.TokenRatio)
			return m
		}
	}
	m.Valid = true
	return m
}

func describePath(path string) string {
	if path == "" {
		return "{} (every method)"
	}
	return path
}

// mcSkips says what a skipped methodConfig entry would have set.
func mcSkips(mc jsonMC) string {
	var parts []string
	if mc.RetryPolicy != nil {
		parts = append(parts, "its retryPolicy")
	}
	if mc.Timeout != nil {
		parts = append(parts, "its timeout")
	}
	if mc.WaitForReady != nil {
		parts = append(parts, "waitForReady")
	}
	if mc.MaxRequestMessageBytes != nil || mc.MaxResponseMessageBytes != nil {
		parts = append(parts, "its message size limits")
	}
	if len(parts) == 0 {
		return ""
	}
	return ", and " + strings.Join(parts, " and ") + " never apply"
}

// schema lists the fields of one object type gRPC decodes, by Go field
// name, and the schema of the fields that are objects or arrays of
// objects.
type schema struct {
	name   string
	fields map[string]*schema
	leaf   map[string]bool
}

var (
	nameSchema     = &schema{name: "name entry", leaf: map[string]bool{"Service": true, "Method": true}}
	retrySchema    = &schema{name: "retryPolicy", leaf: map[string]bool{"MaxAttempts": true, "InitialBackoff": true, "MaxBackoff": true, "BackoffMultiplier": true, "RetryableStatusCodes": true}}
	mcSchema       = &schema{name: "methodConfig entry", fields: map[string]*schema{"Name": nameSchema, "RetryPolicy": retrySchema}, leaf: map[string]bool{"WaitForReady": true, "Timeout": true, "MaxRequestMessageBytes": true, "MaxResponseMessageBytes": true}}
	throttleSchema = &schema{name: "retryThrottling", leaf: map[string]bool{"MaxTokens": true, "TokenRatio": true}}
	healthSchema   = &schema{name: "healthCheckConfig", leaf: map[string]bool{"ServiceName": true}}
	scSchema       = &schema{name: "service config", fields: map[string]*schema{"MethodConfig": mcSchema, "RetryThrottling": throttleSchema, "HealthCheckConfig": healthSchema}, leaf: map[string]bool{"LoadBalancingPolicy": true, "LoadBalancingConfig": true}}
)

// matchField finds the Go field a JSON key decodes into, the way
// encoding/json does: an exact match first, then a case-insensitive one.
func (s *schema) matchField(key string) string {
	all := []string{}
	for f := range s.fields {
		all = append(all, f)
	}
	for f := range s.leaf {
		all = append(all, f)
	}
	sort.Strings(all)
	for _, f := range all {
		if f == key {
			return f
		}
	}
	for _, f := range all {
		if strings.EqualFold(f, key) {
			return f
		}
	}
	return ""
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// walkIgnored records every key gRPC ignores and every key that appears
// twice.
func walkIgnored(n *jnode, s *schema, path string, m *svcModel) {
	if n.kind == 'a' {
		for i, e := range n.elems {
			walkIgnored(e, s, fmt.Sprintf("%s[%d]", path, i), m)
		}
		return
	}
	if n.kind != 'o' {
		return
	}
	seen := map[string]string{}
	for _, mem := range n.members {
		field := s.matchField(mem.key)
		at := mem.key
		if path != "" {
			at = path + "." + mem.key
		}
		if field == "" {
			m.Issues = append(m.Issues, ignoredIssue(s, mem, at, path))
			continue
		}
		if prev, ok := seen[field]; ok {
			m.Issues = append(m.Issues, cfgIssue{
				Severity: "WARNING", Code: "duplicate-field", Off: mem.off, Value: at,
				Message: fmt.Sprintf("%q and %q both set %s; encoding/json, which gRPC uses, applies both in order, so the later one wins or merges into the earlier one", prev, mem.key, lowerFirst(field)),
				Fix:     "keep one of them",
			})
		}
		seen[field] = mem.key
		if sub := s.fields[field]; sub != nil {
			walkIgnored(mem.val, sub, at, m)
		}
	}
}

// where a misplaced key belongs, to explain the common mistakes.
var belongs = map[string]string{
	"name": "methodConfig entry", "timeout": "methodConfig entry", "waitforready": "methodConfig entry", "retrypolicy": "methodConfig entry",
	"maxrequestmessagebytes": "methodConfig entry", "maxresponsemessagebytes": "methodConfig entry",
	"maxattempts": "retryPolicy", "initialbackoff": "retryPolicy", "maxbackoff": "retryPolicy", "backoffmultiplier": "retryPolicy", "retryablestatuscodes": "retryPolicy",
	"service": "name entry", "method": "name entry",
	"methodconfig": "service config", "loadbalancingpolicy": "service config", "loadbalancingconfig": "service config", "retrythrottling": "service config", "healthcheckconfig": "service config",
	"maxtokens": "retryThrottling", "tokenratio": "retryThrottling", "servicename": "healthCheckConfig",
}

// ignoredIssue describes one key gRPC ignores. Keys of one object that
// are ignored for the same reason are merged into one issue later.
func ignoredIssue(s *schema, mem jmember, at, path string) cfgIssue {
	is := cfgIssue{Severity: "ERROR", Code: "ignored-field", Off: mem.off, Value: at, keys: []string{mem.key}}
	squashed := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(mem.key))
	home, known := belongs[squashed]
	switch {
	case known && home == s.name && squashed != strings.ToLower(mem.key):
		is.group = path + "|case"
		is.render = func(keys []string) (string, string) {
			fixes := make([]string, len(keys))
			for i, k := range keys {
				fixes[i] = fmt.Sprintf("%q to %q", k, lowerFirst(s.matchFieldSquashed(strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(k)))))
			}
			return fmt.Sprintf("grpc-go ignores %s: it reads camelCase field names, matched without regard to case, and not snake_case or kebab-case, so %s no effect", quoteList(keys), hasHave(keys)),
				"rename " + strings.Join(fixes, ", ")
		}
	case known && s.name == "service config" && (home == "methodConfig entry" || home == "retryPolicy"):
		is.group = path + "|" + home
		is.render = func(keys []string) (string, string) {
			msg := fmt.Sprintf("a service config has no top-level %s, and grpc-go ignores fields it doesn't know, so %s no effect; %s in a %s", fieldWord(keys), hasHave(keys), belongWord(keys), home)
			if home == "retryPolicy" || has(lowerAll(keys), "retrypolicy") {
				msg += ", so this retry policy is never used"
			}
			return msg, `wrap it: {"methodConfig": [{"name": [{"service": "package.Service"}], "retryPolicy": {...}}]}`
		}
	case known:
		is.group = path + "|" + home
		is.render = func(keys []string) (string, string) {
			return fmt.Sprintf("a %s has no %s (%s in a %s), and grpc-go ignores fields it doesn't know, so %s no effect", s.name, fieldWord(keys), belongWord(keys), home, hasHave(keys)),
				fmt.Sprintf("move %s into the %s", itThem(keys), home)
		}
	default:
		is.group = path + "|unknown|" + mem.key
		is.render = func(keys []string) (string, string) {
			fix := ""
			if near := s.closestField(keys[0]); near != "" {
				fix = fmt.Sprintf("did you mean %q?", lowerFirst(near))
			}
			return fmt.Sprintf("a %s has no field %q, and grpc-go ignores fields it doesn't know, so this has no effect", s.name, keys[0]), fix
		}
	}
	return is
}

func quoteList(keys []string) string {
	q := make([]string, len(keys))
	for i, k := range keys {
		q[i] = fmt.Sprintf("%q", k)
	}
	return strings.Join(q, ", ")
}

func fieldWord(keys []string) string {
	if len(keys) == 1 {
		return "field " + quoteList(keys)
	}
	return "fields " + quoteList(keys)
}

func hasHave(keys []string) string {
	if len(keys) == 1 {
		return "it has"
	}
	return "they have"
}

func belongWord(keys []string) string {
	if len(keys) == 1 {
		return "it belongs"
	}
	return "they belong"
}

func itThem(keys []string) string {
	if len(keys) == 1 {
		return "it"
	}
	return "them"
}

func lowerAll(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = strings.ToLower(k)
	}
	return out
}

// groupIgnored merges the ignored keys of one object that share a reason,
// and writes their messages.
func groupIgnored(issues []cfgIssue) []cfgIssue {
	var out []cfgIssue
	at := map[string]int{}
	for _, is := range issues {
		if is.group == "" {
			out = append(out, is)
			continue
		}
		if i, ok := at[is.group]; ok {
			out[i].keys = append(out[i].keys, is.keys...)
			out[i].Value += ", " + is.Value
			continue
		}
		at[is.group] = len(out)
		out = append(out, is)
	}
	for i := range out {
		if out[i].render != nil {
			out[i].Message, out[i].Fix = out[i].render(out[i].keys)
		}
	}
	return out
}

func (s *schema) matchFieldSquashed(squashed string) string {
	for f := range s.fields {
		if strings.ToLower(f) == squashed {
			return f
		}
	}
	for f := range s.leaf {
		if strings.ToLower(f) == squashed {
			return f
		}
	}
	return squashed
}

func (s *schema) closestField(key string) string {
	best, bestD := "", 3
	names := []string{}
	for f := range s.fields {
		names = append(names, f)
	}
	for f := range s.leaf {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		if d := editDistance(strings.ToLower(f), strings.ToLower(key)); d < bestD {
			best, bestD = f, d
		}
	}
	return best
}

// memberArray returns the elements of the array a key holds, using the
// last key that decodes into the field, as encoding/json does.
func memberArray(n *jnode, field string) []*jnode {
	if n == nil || n.kind != 'o' {
		return nil
	}
	var out []*jnode
	for _, mem := range n.members {
		if mem.key == field || strings.EqualFold(mem.key, field) {
			out = nil
			if mem.val.kind == 'a' {
				out = mem.val.elems
			}
		}
	}
	return out
}

// memberValue returns the value a key holds and the offset of the key, again
// using the last member that decodes into the field, as encoding/json does.
func memberValue(n *jnode, field string) (*jnode, int) {
	if n == nil || n.kind != 'o' {
		return nil, 0
	}
	var out *jnode
	off := 0
	for _, mem := range n.members {
		if mem.key == field || strings.EqualFold(mem.key, field) {
			out, off = mem.val, mem.off
		}
	}
	return out, off
}

// lookup returns the method config gRPC uses for a full method name: an
// exact name, then the service's default, then the default for every
// method.
func (m *svcModel) lookup(full string) *methodCfg {
	if c, ok := m.Methods[full]; ok {
		return c
	}
	if i := strings.LastIndex(full, "/"); i >= 0 {
		if c, ok := m.Methods[full[:i+1]]; ok {
			return c
		}
	}
	return m.Methods[""]
}

func (c *methodCfg) describe() string {
	var parts []string
	if c.Retry != nil {
		p := fmt.Sprintf("retry up to %d attempts on %s", c.Retry.Attempts, strings.Join(c.Retry.Codes, ", "))
		if c.Retry.Requested != c.Retry.Attempts {
			p += fmt.Sprintf(" (asks for %d)", c.Retry.Requested)
		}
		parts = append(parts, p)
	}
	if c.Timeout != nil {
		parts = append(parts, "timeout "+time.Duration(*c.Timeout).String())
	}
	if c.WaitForReady != nil {
		parts = append(parts, fmt.Sprintf("waitForReady %v", *c.WaitForReady))
	}
	if c.MaxReq != nil {
		parts = append(parts, fmt.Sprintf("max request %d bytes", *c.MaxReq))
	}
	if c.MaxResp != nil {
		parts = append(parts, fmt.Sprintf("max response %d bytes", *c.MaxResp))
	}
	if len(parts) == 0 {
		return "nothing set"
	}
	return strings.Join(parts, ", ")
}
