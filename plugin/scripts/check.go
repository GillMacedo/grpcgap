package main

import (
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type finding struct {
	Severity string   `json:"severity"`
	Code     string   `json:"code"`
	Place    string   `json:"place"`
	Value    string   `json:"value"`
	Config   string   `json:"serviceConfig,omitempty"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
	Programs []string `json:"programs"`
}

type applied struct {
	Name     string `json:"name"`
	Settings string `json:"settings"`
}

type configReport struct {
	Place   string    `json:"place"`
	Calls   []string  `json:"calls,omitempty"`
	Valid   bool      `json:"valid"`
	Error   string    `json:"error,omitempty"`
	Applies []applied `json:"applies"`
	Other   []string  `json:"otherSettings"`
}

type serviceReport struct {
	Name    string   `json:"name"`
	Methods []string `json:"methods"`
	Package string   `json:"package"`
}

type report struct {
	Tool       string          `json:"tool"`
	Dir        string          `json:"dir"`
	GRPC       string          `json:"grpcModule"`
	Programs   []string        `json:"programs"`
	Services   []serviceReport `json:"services"`
	MethodRefs int             `json:"methodNamesChecked"`
	Configs    []configReport  `json:"serviceConfigs"`
	NotChecked []unchecked     `json:"notChecked"`
	Notes      []string        `json:"notes"`
	Findings   []finding       `json:"findings"`
}

// check reads the packages go list returned and the extra service config
// files, and builds the report.
func check(root string, list []*listPkg, configFiles []*configSite) (*report, error) {
	rep := &report{Tool: "grpcgap " + version}
	var grpcPkg *listPkg
	var own []*listPkg
	for _, p := range list {
		if p.ImportPath == grpcPath {
			grpcPkg = p
		}
		if !p.Standard && p.Module != nil && p.Module.Main {
			own = append(own, p)
		}
	}
	if grpcPkg == nil {
		return nil, refuse("none of the packages read links %s, so there is nothing to check", grpcPath)
	}
	if len(own) == 0 {
		return nil, refuse("none of the packages read belongs to the main module, so there is no code of yours to check")
	}
	if m := grpcPkg.Module; m != nil {
		rep.GRPC = m.Path
		if m.Version != "" {
			rep.GRPC += " " + m.Version
		}
		if m.Replace != nil {
			rep.GRPC += " => " + m.Replace.Path
			if m.Replace.Version != "" {
				rep.GRPC += " " + m.Replace.Version
			}
		}
	} else {
		rep.GRPC = grpcPath
	}

	programs := map[string][]string{}
	for _, p := range list {
		if p.DepOnly || p.Name != "main" {
			continue
		}
		rep.Programs = append(rep.Programs, p.ImportPath)
		for _, d := range append([]string{p.ImportPath}, p.Deps...) {
			programs[d] = append(programs[d], p.ImportPath)
		}
	}
	sort.Strings(rep.Programs)

	s := &scanner{fset: token.NewFileSet(), root: root, pkgs: map[string]*pkgSrc{}}
	var withGRPC []*pkgSrc
	for _, p := range list {
		imports := false
		for _, im := range p.Imports {
			if im == grpcPath {
				imports = true
			}
		}
		if !imports && !(p.Module != nil && p.Module.Main && !p.Standard) {
			continue
		}
		ps, err := s.parsePkg(p)
		if err != nil {
			return nil, err
		}
		if imports {
			withGRPC = append(withGRPC, ps)
		}
	}
	for _, ps := range withGRPC {
		s.findServices(ps)
	}
	sort.Slice(own, func(i, j int) bool { return own[i].ImportPath < own[j].ImportPath })
	for _, p := range own {
		s.scanOwn(s.pkgs[p.ImportPath])
	}

	known := map[string]*service{}
	for _, svc := range s.services {
		if prev, ok := known[svc.Name]; ok {
			prev.Methods = union(prev.Methods, svc.Methods)
			continue
		}
		cp := *svc
		known[svc.Name] = &cp
	}
	names := make([]string, 0, len(known))
	for n := range known {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rep.Services = append(rep.Services, serviceReport{Name: n, Methods: known[n].Methods, Package: known[n].Pkg})
	}
	c := &checker{known: known, names: names, programs: programs}

	for _, ref := range s.refs {
		f := c.reference(ref)
		if f == nil {
			continue
		}
		rep.MethodRefs++
		if f.Code != "" {
			rep.Findings = append(rep.Findings, *f)
		}
	}

	s.configs = append(s.configs, configFiles...)

	for _, site := range s.configs {
		if site.parseNote != "" && !site.Direct {
			rep.Notes = append(rep.Notes, site.Place+": "+site.parseNote+", so it isn't checked")
			continue
		}
		cr, fs, notes := c.config(site, s.maxCalls)
		rep.Configs = append(rep.Configs, cr)
		rep.Findings = append(rep.Findings, fs...)
		rep.Notes = append(rep.Notes, notes...)
	}
	rep.NotChecked = s.skipped
	rep.Notes = append(rep.Notes, s.notes...)
	sort.SliceStable(rep.Findings, func(i, j int) bool { return placeLess(rep.Findings[i].Place, rep.Findings[j].Place) })
	return rep, nil
}

func placeLess(a, b string) bool {
	fa, la := splitPlace(a)
	fb, lb := splitPlace(b)
	if fa != fb {
		return fa < fb
	}
	return la < lb
}

func splitPlace(p string) (string, int) {
	i := strings.LastIndex(p, ":")
	if i < 0 {
		return p, 0
	}
	n := 0
	fmt.Sscanf(p[i+1:], "%d", &n)
	return p[:i], n
}

type checker struct {
	known    map[string]*service
	names    []string
	programs map[string][]string
}

func union(a, b []string) []string {
	set := map[string]bool{}
	for _, x := range append(append([]string{}, a...), b...) {
		set[x] = true
	}
	out := make([]string, 0, len(set))
	for x := range set {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func upperStart(s string) bool {
	return s != "" && unicode.IsUpper(rune(s[0]))
}

// splitService splits "pkg.v1.Service" into "pkg.v1" and "Service".
func splitService(name string) (string, string) {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return "", name
	}
	return name[:i], name[i+1:]
}

// isCandidate says whether a string that names no known service still
// looks like a gRPC name, rather than a URL path.
func (c *checker) isCandidate(svc, method string) bool {
	pkg, simple := splitService(svc)
	if pkg == "" {
		return false
	}
	for _, n := range c.names {
		if p, _ := splitService(n); p == pkg {
			return true
		}
	}
	return upperStart(simple) && (method == "" || upperStart(method))
}

// reference checks one method name string. It returns nil for strings
// that aren't gRPC names, and a finding with no code for names that match.
func (c *checker) reference(ref *reference) *finding {
	svc, ok := c.known[ref.Service]
	if ok && (ref.Method == "" || has(svc.Methods, ref.Method)) {
		return &finding{}
	}
	if !ok && !c.isCandidate(ref.Service, ref.Method) {
		return nil
	}
	f := &finding{Severity: "ERROR", Place: ref.Place, Value: fmt.Sprintf("%q", ref.Value), Programs: c.programsOf(ref.Pkg)}
	what := "this string never equals the method name gRPC gives interceptors, stats handlers and Invoke, so a check or lookup keyed by it never matches"
	if ref.Method == "" {
		what = "no method name starts with this prefix, so a check that compares method names with it never matches"
	}
	if ok {
		f.Code = "unknown-method"
		f.Message = fmt.Sprintf("service %s has no method %s: %s", ref.Service, ref.Method, what)
		f.Fix = c.methodFix(svc, ref.Method, true)
		return f
	}
	f.Code = "unknown-service"
	f.Message = fmt.Sprintf("no linked package defines a gRPC service %s: %s", ref.Service, what)
	f.Fix = c.serviceFix(ref.Service, ref.Method, true)
	return f
}

func (c *checker) programsOf(pkg string) []string {
	ps := append([]string{}, c.programs[pkg]...)
	sort.Strings(ps)
	if ps == nil {
		ps = []string{}
	}
	return ps
}

// methodFix suggests a replacement for a method a service doesn't have.
func (c *checker) methodFix(svc *service, method string, full bool) string {
	var owners []string
	for _, n := range c.names {
		if n != svc.Name && has(c.known[n].Methods, method) {
			owners = append(owners, n)
		}
	}
	if len(owners) > 0 {
		if full {
			return fmt.Sprintf("%s is a method of %s: use %q", method, strings.Join(owners, " and "), "/"+owners[0]+"/"+method)
		}
		return fmt.Sprintf("%s is a method of %s: name that service instead", method, strings.Join(owners, " and "))
	}
	if near := closest(method, svc.Methods); near != "" {
		if full {
			return fmt.Sprintf("did you mean %q?", "/"+svc.Name+"/"+near)
		}
		return fmt.Sprintf("did you mean %q?", near)
	}
	return fmt.Sprintf("%s's methods include %s", svc.Name, nearest(method, svc.Methods, 6))
}

// nearest lists up to n candidates, the most similar to s first.
func nearest(s string, candidates []string, n int) string {
	sorted := append([]string{}, candidates...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return editDistance(strings.ToLower(s), strings.ToLower(sorted[i])) < editDistance(strings.ToLower(s), strings.ToLower(sorted[j]))
	})
	if len(sorted) <= n {
		return strings.Join(sorted, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(sorted[:n], ", "), len(sorted)-n)
}

// sharedPrefix counts the dotted segments two service names share.
func sharedPrefix(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := 0
	for n < len(as) && n < len(bs) && as[n] == bs[n] {
		n++
	}
	return n
}

// serviceFix suggests a replacement for a service nothing defines.
func (c *checker) serviceFix(name, method string, full bool) string {
	_, simple := splitService(name)
	var same []string
	for _, n := range c.names {
		if _, s := splitService(n); s == simple {
			same = append(same, n)
		}
	}
	pick := ""
	if len(same) == 1 {
		pick = same[0]
	} else if near := closest(name, c.names); near != "" {
		pick = near
	}
	if pick != "" {
		if full && method != "" && has(c.known[pick].Methods, method) {
			return fmt.Sprintf("did you mean %q?", "/"+pick+"/"+method)
		}
		if full && method == "" {
			return fmt.Sprintf("did you mean %q?", "/"+pick+"/")
		}
		if !full {
			return fmt.Sprintf("did you mean %q?", pick)
		}
		return fmt.Sprintf("did you mean service %s? Its methods include %s", pick, nearest(method, c.known[pick].Methods, 6))
	}
	if len(c.names) == 0 {
		return "no grpc.ServiceDesc was found in the linked packages; check that the generated code for this service is linked"
	}
	best := 0
	for _, n := range c.names {
		if p := sharedPrefix(name, n); p > best {
			best = p
		}
	}
	if best == 0 {
		return fmt.Sprintf("no linked service shares its package; the linked services include %s. If this name is used on purpose for something that isn't a gRPC method, mark it //grpcgap:ignore", nearest(name, c.names, 4))
	}
	var related []string
	for _, n := range c.names {
		if sharedPrefix(name, n) == best {
			related = append(related, n)
		}
	}
	prefix := strings.Join(strings.Split(name, ".")[:best], ".")
	return fmt.Sprintf("linked services under %s: %s. If this name is used on purpose for something that isn't a gRPC method, mark it //grpcgap:ignore", prefix, nearest(name, related, 4))
}

// closest returns the candidate nearest to s by edit distance, if it is
// near enough to be a typo.
func closest(s string, candidates []string) string {
	limit := len(s) / 4
	if limit < 2 {
		limit = 2
	}
	best, bestD := "", limit+1
	for _, c := range candidates {
		if d := editDistance(strings.ToLower(s), strings.ToLower(c)); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// lineAt maps a byte offset in a service config's text to a place.
func (site *configSite) lineAt(off int) string {
	base, line := splitPlace(site.Place)
	if site.IsFile {
		base, line = site.Place, 1
	}
	if site.Raw && off >= 0 && off <= len(site.Text) {
		line += strings.Count(site.Text[:off], "\n")
	}
	return fmt.Sprintf("%s:%d", base, line)
}

// config checks one service config.
func (c *checker) config(site *configSite, maxCalls bool) (configReport, []finding, []string) {
	m := readServiceConfig(site.Text)
	cr := configReport{Place: site.Place, Calls: site.Calls, Valid: m.Valid, Applies: []applied{}, Other: []string{}}
	var progs []string
	if site.Pkg != "" {
		progs = c.programsOf(site.Pkg)
	}
	var fs []finding
	add := func(sev, code string, off int, value, msg, fix string) {
		fs = append(fs, finding{Severity: sev, Code: code, Place: site.lineAt(off), Value: value, Config: site.Place, Message: msg, Fix: fix, Programs: progs})
	}
	for _, is := range m.Issues {
		add(is.Severity, is.Code, is.Off, is.Value, is.Message, is.Fix)
	}
	if !m.Valid {
		cr.Error = m.Err
		msg := "gRPC rejects this service config: " + m.Err
		if site.Direct {
			msg = fmt.Sprintf("grpc.NewClient and grpc.Dial fail with \"grpc: the provided default service config is invalid: ...\" because of this service config: %s", m.Err)
		} else {
			msg += ". A client given it with grpc.WithDefaultServiceConfig fails to start, and a resolver update carrying it is rejected"
		}
		add("ERROR", "invalid-service-config", m.ErrOff, "service config", msg, "")
		return cr, fs, nil
	}
	for _, n := range m.Names {
		cfg := m.Methods[n.Path]
		label := fmt.Sprintf("methodConfig[%d] name {\"service\": %q", n.MC, n.Service)
		if n.Method != "" {
			label += fmt.Sprintf(", \"method\": %q", n.Method)
		}
		label += "}"
		if n.Service == "" {
			label = fmt.Sprintf("methodConfig[%d] name {}", n.MC)
		}
		lost := cfg.describe()
		if n.Service == "" {
			cr.Applies = append(cr.Applies, applied{Name: "every method", Settings: lost})
			continue
		}
		svc, ok := c.known[n.Service]
		switch {
		case !ok:
			add("ERROR", "unknown-service", n.Off, label,
				fmt.Sprintf("no linked package defines a gRPC service %s, so this name matches no call, and its settings (%s) never apply through it", n.Service, lost),
				c.serviceFix(n.Service, n.Method, false))
		case n.Method != "" && !has(svc.Methods, n.Method):
			add("ERROR", "unknown-method", n.Off, label,
				fmt.Sprintf("service %s has no method %s, so this name matches no call, and its settings (%s) never apply through it", n.Service, n.Method, lost),
				c.methodFix(svc, n.Method, false))
		case n.Method == "":
			cr.Applies = append(cr.Applies, applied{Name: "/" + n.Service + "/*", Settings: lost})
		default:
			cr.Applies = append(cr.Applies, applied{Name: n.Path, Settings: lost})
		}
	}
	for i, mc := range m.MCs {
		if mc == nil || mc.Retry == nil {
			continue
		}
		if mc.Retry.Requested > defaultMaxCallAttempts && !maxCalls {
			add("WARNING", "retry-capped", m.MCOffs[i], fmt.Sprintf("methodConfig[%d].retryPolicy.maxAttempts", i),
				fmt.Sprintf("maxAttempts is %d, but grpc-go makes at most %d attempts unless the client sets grpc.WithMaxCallAttempts", mc.Retry.Requested, defaultMaxCallAttempts),
				fmt.Sprintf("set maxAttempts to %d or less, or add grpc.WithMaxCallAttempts(%d) to the dial options", defaultMaxCallAttempts, mc.Retry.Requested))
		}
		if site.NoRetry {
			add("WARNING", "retry-disabled", m.MCOffs[i], fmt.Sprintf("methodConfig[%d].retryPolicy", i),
				"the same dial options include grpc.WithDisableRetry(), so this retry policy is never used",
				"remove grpc.WithDisableRetry() or the retry policy")
		}
	}
	cr.Other = append(cr.Other, m.Other...)
	// grpc-go applies retryThrottling only to calls it retries, so with no
	// retry policy anywhere the policy can't do anything. Say so here rather
	// than leave the config looking as if it throttles retries.
	if m.ThrottleOff > 0 && !m.Retries {
		add("WARNING", "throttling-without-retry", m.ThrottleOff, "retryThrottling",
			"no methodConfig entry has a retryPolicy, and gRPC only consults retryThrottling when it retries a call, so this setting never has any effect",
			`add a "retryPolicy" to the methodConfig entries that should retry, or remove retryThrottling`)
	}
	var notes []string
	if m.LBConfig {
		notes = append(notes, site.Place+": loadBalancingConfig isn't checked; gRPC rejects the whole service config if none of the policies it lists is registered in the program")
	}
	if !m.Sets && len(fs) == 0 {
		add("WARNING", "empty-service-config", 0, "service config", "this service config sets nothing gRPC uses", "remove it, or add the methodConfig entries it was meant to have")
	}
	sort.Slice(cr.Applies, func(i, j int) bool { return cr.Applies[i].Name < cr.Applies[j].Name })
	return cr, fs, notes
}

// readConfigFiles reads files supplied directly or through a --config directory.
func readConfigFiles(paths []string) ([]*configSite, error) {
	var sites []*configSite
	for _, path := range paths {
		files, err := configPaths(path)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			site, err := readConfigFile(file)
			if err != nil {
				return nil, err
			}
			sites = append(sites, site)
		}
	}
	return sites, nil
}

func configPaths(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, refuse("can't read --config %s: %v", path, err)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}

	entries, err := os.ReadDir(path) // Sorted by filename.
	if err != nil {
		return nil, refuse("can't read --config %s: %v", path, err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		file := filepath.Join(path, entry.Name())
		info, err := os.Stat(file)
		if err != nil {
			return nil, refuse("can't read --config %s: %v", file, err)
		}
		if info.IsDir() {
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, refuse("--config %s is not a regular file", file)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return nil, refuse("--config directory %s contains no .json files", path)
	}
	return files, nil
}

// readConfigFile refuses anything other than one complete, non-empty JSON object.
func readConfigFile(path string) (*configSite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, refuse("can't read --config %s: %v", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, refuse("--config %s is empty", path)
	}
	tree, err := parseJSON(data)
	if err != nil {
		return nil, refuse("--config %s isn't one complete JSON value (cut off, or not JSON): %v", path, err)
	}
	if tree.kind != 'o' {
		return nil, refuse("--config %s holds a JSON %s, not a service config object", path, tree.kindName())
	}
	if len(tree.members) == 0 {
		return nil, refuse("--config %s is an empty object, so there is nothing to check", path)
	}
	return &configSite{Text: string(data), Place: filepath.ToSlash(path), Line: 1, File: path, Raw: true, IsFile: true}, nil
}
