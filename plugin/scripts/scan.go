package main

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const grpcPath = "google.golang.org/grpc"

// service is one gRPC service a linked package describes with a
// grpc.ServiceDesc.
type service struct {
	Name    string
	Methods []string
	Pkg     string
	Place   string
}

// pkgSrc is a parsed package.
type pkgSrc struct {
	list  *listPkg
	files []*ast.File
	names []string
}

// reference is a string in the program's own code that names a gRPC
// method or service.
type reference struct {
	Value   string
	Service string
	Method  string
	Place   string
	Pkg     string
}

// configSite is a service config found in the program's own code.
type configSite struct {
	Text      string
	Place     string
	Line      int
	File      string
	Raw       bool // a single raw string literal or a file, so offsets map to lines
	IsFile    bool // given with --config
	Direct    bool // passed to grpc.WithDefaultServiceConfig
	NoRetry   bool // in the same call or list as grpc.WithDisableRetry()
	Pkg       string
	Calls     []string
	litPos    token.Pos
	parseNote string
}

// unchecked is a grpc.WithDefaultServiceConfig argument grpcgap can't
// read without running the program.
type unchecked struct {
	Place string `json:"place"`
	Expr  string `json:"expr"`
}

type scanner struct {
	fset     *token.FileSet
	root     string
	pkgs     map[string]*pkgSrc
	services []*service
	refs     []*reference
	configs  []*configSite
	skipped  []unchecked
	notes    []string
	maxCalls bool // grpc.WithMaxCallAttempts appears in the program's code
}

var fullMethodRe = regexp.MustCompile(`^/([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)/([A-Za-z_][A-Za-z0-9_]*)?$`)

// parsePkg parses the Go files go list says the package builds.
func (s *scanner) parsePkg(p *listPkg) (*pkgSrc, error) {
	if ps, ok := s.pkgs[p.ImportPath]; ok {
		return ps, nil
	}
	ps := &pkgSrc{list: p}
	for _, f := range append(append([]string{}, p.GoFiles...), p.CgoFiles...) {
		path := filepath.Join(p.Dir, f)
		file, err := parser.ParseFile(s.fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, refuse("can't parse %s, which go list says builds: %v", path, err)
		}
		ps.files = append(ps.files, file)
		ps.names = append(ps.names, path)
	}
	s.pkgs[p.ImportPath] = ps
	return ps, nil
}

func (s *scanner) place(pos token.Pos) string {
	p := s.fset.Position(pos)
	rel, err := filepath.Rel(s.root, p.Filename)
	if err != nil {
		rel = p.Filename
	}
	return filepath.ToSlash(rel) + ":" + strconv.Itoa(p.Line)
}

// importName returns the name a file uses for an import path, or "" if
// the file doesn't import it.
func importName(f *ast.File, path string) string {
	for _, im := range f.Imports {
		p, err := strconv.Unquote(im.Path.Value)
		if err != nil || p != path {
			continue
		}
		if im.Name != nil {
			return im.Name.Name
		}
		return filepath.Base(path)
	}
	return ""
}

// isSel reports whether e is pkgName.sel.
func isSel(e ast.Expr, pkgName, sel string) bool {
	se, ok := e.(*ast.SelectorExpr)
	if !ok || pkgName == "" {
		return false
	}
	id, ok := se.X.(*ast.Ident)
	return ok && id.Name == pkgName && se.Sel.Name == sel
}

// findServices reads every grpc.ServiceDesc composite literal in a linked
// package.
func (s *scanner) findServices(ps *pkgSrc) {
	for _, f := range ps.files {
		g := importName(f, grpcPath)
		if g == "" || g == "_" || g == "." {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok || !isSel(cl.Type, g, "ServiceDesc") {
				return true
			}
			svc := &service{Pkg: ps.list.ImportPath, Place: s.place(cl.Pos())}
			ok = true
			for _, el := range cl.Elts {
				kv, isKV := el.(*ast.KeyValueExpr)
				if !isKV {
					ok = false
					continue
				}
				key, _ := kv.Key.(*ast.Ident)
				if key == nil {
					continue
				}
				switch key.Name {
				case "ServiceName":
					v, good := s.constValue(ps, f, kv.Value, 0)
					if !good {
						ok = false
					}
					svc.Name = v
				case "Methods", "Streams":
					field := "MethodName"
					if key.Name == "Streams" {
						field = "StreamName"
					}
					list, isList := kv.Value.(*ast.CompositeLit)
					if !isList {
						ok = false
						continue
					}
					for _, m := range list.Elts {
						mcl, isCL := m.(*ast.CompositeLit)
						if !isCL {
							ok = false
							continue
						}
						for _, mel := range mcl.Elts {
							mkv, isMKV := mel.(*ast.KeyValueExpr)
							if !isMKV {
								continue
							}
							if mk, _ := mkv.Key.(*ast.Ident); mk != nil && mk.Name == field {
								v, good := s.constValue(ps, f, mkv.Value, 0)
								if !good {
									ok = false
								}
								svc.Methods = append(svc.Methods, v)
							}
						}
					}
				}
			}
			if !ok || svc.Name == "" {
				s.notes = append(s.notes, "a grpc.ServiceDesc at "+s.place(cl.Pos())+" isn't made of constant names, so its service and methods are unknown to grpcgap, and strings naming them are reported as unknown")
				return true
			}
			sort.Strings(svc.Methods)
			s.services = append(s.services, svc)
			return true
		})
	}
}

// constValue evaluates a string constant expression: literals, + and
// names of package-level or local constants and variables with a
// constant initializer, in this package or another one that was parsed.
func (s *scanner) constValue(ps *pkgSrc, f *ast.File, e ast.Expr, depth int) (string, bool) {
	if depth > 20 {
		return "", false
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(x.Value)
		return v, err == nil
	case *ast.ParenExpr:
		return s.constValue(ps, f, x.X, depth+1)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok1 := s.constValue(ps, f, x.X, depth+1)
		b, ok2 := s.constValue(ps, f, x.Y, depth+1)
		return a + b, ok1 && ok2
	case *ast.Ident:
		if x.Obj != nil {
			if vs, ok := x.Obj.Decl.(*ast.ValueSpec); ok {
				return s.specValue(ps, f, vs, x.Name, depth)
			}
			if as, ok := x.Obj.Decl.(*ast.AssignStmt); ok && as.Tok == token.DEFINE && len(as.Lhs) == len(as.Rhs) {
				for i, l := range as.Lhs {
					if id, ok := l.(*ast.Ident); ok && id.Name == x.Name {
						return s.constValue(ps, f, as.Rhs[i], depth+1)
					}
				}
			}
			return "", false
		}
		for i, pf := range ps.files {
			for _, d := range pf.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
					continue
				}
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec)
					for _, n := range vs.Names {
						if n.Name == x.Name {
							return s.specValue(ps, ps.files[i], vs, x.Name, depth)
						}
					}
				}
			}
		}
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		for _, im := range f.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			other := s.pkgs[path]
			if other == nil {
				continue
			}
			name := other.list.Name
			if im.Name != nil {
				name = im.Name.Name
			}
			if name == id.Name {
				return s.constValue(other, nil, ast.NewIdent(x.Sel.Name), depth+1)
			}
		}
	}
	return "", false
}

func (s *scanner) specValue(ps *pkgSrc, f *ast.File, vs *ast.ValueSpec, name string, depth int) (string, bool) {
	for i, n := range vs.Names {
		if n.Name == name && i < len(vs.Values) {
			return s.constValue(ps, f, vs.Values[i], depth+1)
		}
	}
	return "", false
}

// singleLit returns the string literal an expression resolves to when it
// is exactly one literal, so findings can point at lines inside it.
func (s *scanner) singleLit(ps *pkgSrc, f *ast.File, e ast.Expr, depth int) *ast.BasicLit {
	if depth > 20 {
		return nil
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		return x
	case *ast.ParenExpr:
		return s.singleLit(ps, f, x.X, depth+1)
	case *ast.Ident:
		var vs *ast.ValueSpec
		if x.Obj != nil {
			if as, ok := x.Obj.Decl.(*ast.AssignStmt); ok && as.Tok == token.DEFINE && len(as.Lhs) == len(as.Rhs) {
				for i, l := range as.Lhs {
					if id, ok := l.(*ast.Ident); ok && id.Name == x.Name {
						return s.singleLit(ps, f, as.Rhs[i], depth+1)
					}
				}
			}
			vs, _ = x.Obj.Decl.(*ast.ValueSpec)
		} else {
			for _, pf := range ps.files {
				for _, d := range pf.Decls {
					if gd, ok := d.(*ast.GenDecl); ok {
						for _, sp := range gd.Specs {
							if v, ok := sp.(*ast.ValueSpec); ok {
								for _, n := range v.Names {
									if n.Name == x.Name {
										vs = v
									}
								}
							}
						}
					}
				}
			}
		}
		if vs == nil {
			return nil
		}
		for i, n := range vs.Names {
			if n.Name == x.Name && i < len(vs.Values) {
				return s.singleLit(ps, f, vs.Values[i], depth+1)
			}
		}
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok || f == nil {
			return nil
		}
		for _, im := range f.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			other := s.pkgs[path]
			if other == nil {
				continue
			}
			name := other.list.Name
			if im.Name != nil {
				name = im.Name.Name
			}
			if name == id.Name {
				return s.singleLit(other, nil, ast.NewIdent(x.Sel.Name), depth+1)
			}
		}
	}
	return nil
}

// ignored reports whether a line has a //grpcgap:ignore comment on it or
// on the line above, or is inside a declaration whose doc comment has one.
func (s *scanner) ignored(f *ast.File, pos token.Pos) bool {
	for _, d := range f.Decls {
		var doc *ast.CommentGroup
		switch x := d.(type) {
		case *ast.GenDecl:
			doc = x.Doc
		case *ast.FuncDecl:
			doc = x.Doc
		}
		if doc == nil || pos < d.Pos() || pos >= d.End() {
			continue
		}
		for _, c := range doc.List {
			if strings.HasPrefix(c.Text, "//grpcgap:ignore") {
				return true
			}
		}
	}
	line := s.fset.Position(pos).Line
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if !strings.HasPrefix(c.Text, "//grpcgap:ignore") {
				continue
			}
			l := s.fset.Position(c.Pos()).Line
			if l == line || l == line-1 {
				return true
			}
		}
	}
	return false
}

// configKeys are the keys that make a JSON string literal a service
// config, compared after removing case, _ and -.
var configKeys = map[string]bool{
	"methodconfig": true, "retrypolicy": true, "retrythrottling": true, "loadbalancingpolicy": true,
	"loadbalancingconfig": true, "healthcheckconfig": true,
}

func looksLikeServiceConfig(text string) (bool, bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "{") {
		return false, false
	}
	tree, err := parseJSON([]byte(t))
	if err != nil {
		low := strings.ToLower(t)
		return false, strings.Contains(low, "methodconfig") || strings.Contains(low, "retrypolicy")
	}
	if tree.kind != 'o' {
		return false, false
	}
	for _, m := range tree.members {
		if configKeys[strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(m.key))] {
			return true, false
		}
	}
	return false, false
}

// scanOwn reads one of the program's own packages for method names and
// service configs.
func (s *scanner) scanOwn(ps *pkgSrc) {
	for _, f := range ps.files {
		if ast.IsGenerated(f) {
			continue
		}
		g := importName(f, grpcPath)
		seen := map[token.Pos]bool{}
		// Service configs passed to grpc.WithDefaultServiceConfig.
		ast.Inspect(f, func(n ast.Node) bool {
			var list []ast.Expr
			switch x := n.(type) {
			case *ast.CallExpr:
				if isSel(x.Fun, g, "WithMaxCallAttempts") {
					s.maxCalls = true
				}
				list = x.Args
			case *ast.CompositeLit:
				list = x.Elts
			default:
				return true
			}
			noRetry := false
			for _, a := range list {
				if c, ok := a.(*ast.CallExpr); ok && isSel(c.Fun, g, "WithDisableRetry") {
					noRetry = true
				}
			}
			for _, a := range list {
				c, ok := a.(*ast.CallExpr)
				if !ok || !isSel(c.Fun, g, "WithDefaultServiceConfig") || len(c.Args) != 1 {
					continue
				}
				seen[c.Pos()] = true
				s.addDirect(ps, f, c, noRetry)
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if ok && isSel(c.Fun, g, "WithDefaultServiceConfig") && len(c.Args) == 1 && !seen[c.Pos()] {
				seen[c.Pos()] = true
				s.addDirect(ps, f, c, false)
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if m := fullMethodRe.FindStringSubmatch(v); m != nil {
				if !s.ignored(f, lit.Pos()) {
					s.refs = append(s.refs, &reference{Value: v, Service: m[1], Method: m[2], Place: s.place(lit.Pos()), Pkg: ps.list.ImportPath})
				}
				return true
			}
			if is, broken := looksLikeServiceConfig(v); is || broken {
				if s.ignored(f, lit.Pos()) {
					return true
				}
				for _, c := range s.configs {
					if c.litPos == lit.Pos() {
						return true
					}
				}
				site := &configSite{Text: v, Place: s.place(lit.Pos()), Line: s.fset.Position(lit.Pos()).Line, File: s.fset.Position(lit.Pos()).Filename, Raw: strings.HasPrefix(lit.Value, "`"), Pkg: ps.list.ImportPath, litPos: lit.Pos()}
				if broken {
					site.parseNote = "looks like a service config but isn't valid JSON as written (it may be a template filled in at run time)"
				}
				s.configs = append(s.configs, site)
			}
			return true
		})
	}
}

// addDirect records the argument of a grpc.WithDefaultServiceConfig call.
func (s *scanner) addDirect(ps *pkgSrc, f *ast.File, c *ast.CallExpr, noRetry bool) {
	arg := c.Args[0]
	if s.ignored(f, c.Pos()) {
		return
	}
	text, ok := s.constValue(ps, f, arg, 0)
	if !ok {
		s.skipped = append(s.skipped, unchecked{Place: s.place(c.Pos()), Expr: exprString(s.fset, arg)})
		return
	}
	lit := s.singleLit(ps, f, arg, 0)
	pos := arg.Pos()
	raw := false
	if lit != nil {
		pos = lit.Pos()
		raw = strings.HasPrefix(lit.Value, "`")
	}
	for _, site := range s.configs {
		if site.litPos == pos {
			site.Direct = true
			site.NoRetry = site.NoRetry || noRetry
			site.Calls = appendUnique(site.Calls, s.place(c.Pos()))
			return
		}
	}
	p := s.fset.Position(pos)
	s.configs = append(s.configs, &configSite{Text: text, Place: s.place(pos), Line: p.Line, File: p.Filename, Raw: raw, Direct: true, NoRetry: noRetry, Pkg: ps.list.ImportPath, Calls: []string{s.place(c.Pos())}, litPos: pos})
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, e); err != nil {
		return "(expression)"
	}
	return oneLine(b.String())
}
