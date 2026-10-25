package httpcontract

import (
	"context"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/Xwudao/weld/internal/project"
	"golang.org/x/tools/go/packages"
)

// ExportOptions optionally selects one module and overrides its mount prefix.
// A prefix override is explicit: it is useful for externally mounted services.
type ExportOptions struct {
	Module string
	Prefix *string
}

// Export type-checks source but never calls a constructor, Register, or InitApp.
// V1 supports straight-line Register functions and statically evaluable mounts.
func Export(ctx context.Context, dir string, opts ExportOptions) (*Contract, error) {
	m, err := project.Load(dir)
	if err != nil {
		return nil, err
	}
	if opts.Prefix != nil && opts.Module == "" {
		return nil, fmt.Errorf("--prefix requires --module")
	}
	var modules []string
	for _, mod := range m.Modules {
		if opts.Module == "" || mod.Name == opts.Module {
			modules = append(modules, mod.Name)
		}
	}
	if len(modules) == 0 {
		return nil, fmt.Errorf("no HTTP modules selected; add a module or check --module")
	}
	pkgs, server, err := loadHTTPSource(ctx, dir, m, modules)
	if err != nil {
		return nil, err
	}
	c := &Contract{Version: Version, Service: m.Name}
	ex := &exporter{contract: c, httpx: m.Module + "/internal/httpx", defs: map[string]Definition{}, named: map[*types.Named]string{}, owners: map[string]*types.Named{}, packages: map[*types.Package]*packages.Package{}}
	packages.Visit(pkgs, func(p *packages.Package) bool { ex.packages[p.Types] = p; return true }, nil)
	for _, p := range pkgs {
		if p == server {
			continue
		}
		prefix := ""
		if opts.Prefix != nil {
			prefix = *opts.Prefix
		} else {
			name, ok := p.Types.Scope().Lookup("Name").(*types.Const)
			if !ok || name.Val().Kind() != constant.String {
				return nil, fmt.Errorf("module %s needs a constant Name or an explicit --prefix", p.Name)
			}
			prefix, err = modulePrefix(server, constant.StringVal(name.Val()))
			if err != nil {
				return nil, fmt.Errorf("module %s mount: %w (use --module %s --prefix <path> for an explicit mount)", p.Name, err, p.Name)
			}
		}
		if err := ex.module(p, prefix); err != nil {
			return nil, err
		}
	}
	for _, d := range ex.defs {
		c.Types = append(c.Types, d)
	}
	sort.Slice(c.Types, func(i, j int) bool { return c.Types[i].Name < c.Types[j].Name })
	sort.Slice(c.Operations, func(i, j int) bool { return c.Operations[i].Name < c.Operations[j].Name })
	sort.Strings(c.Skipped)
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// loadHTTPSource type-checks internal/httpserver and the named modules of the
// project in dir. Any type error fails the load: a reader working from broken
// source would report routes that do not exist.
func loadHTTPSource(ctx context.Context, dir string, m *project.Manifest, modules []string) ([]*packages.Package, *packages.Package, error) {
	patterns := []string{"./internal/httpserver"}
	for _, name := range modules {
		patterns = append(patterns, "./internal/modules/"+name)
	}
	pkgs, err := packages.Load(&packages.Config{
		Context: ctx, Dir: dir, Env: append(os.Environ(), "GOWORK=off"),
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
	}, patterns...)
	if err != nil {
		return nil, nil, fmt.Errorf("load HTTP source: %w", err)
	}
	var errors []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			errors = append(errors, e.Error())
		}
	})
	if len(errors) > 0 {
		sort.Strings(errors)
		return nil, nil, fmt.Errorf("HTTP source does not type-check (run go mod tidy if dependencies are missing):\n%s", strings.Join(errors, "\n"))
	}
	for _, p := range pkgs {
		if p.PkgPath == m.Module+"/internal/httpserver" {
			return pkgs, p, nil
		}
	}
	return nil, nil, fmt.Errorf("HTTP server package not found")
}

type exporter struct {
	// routesOnly reads routes without their DTO shapes, and records raw routes
	// instead of skipping them: it is the mode `weld routes` lists the surface
	// in, which needs neither a contract-compatible DTO nor a typed handler.
	routesOnly bool
	raw        []Route
	contract   *Contract
	httpx      string
	defs       map[string]Definition
	named      map[*types.Named]string
	owners     map[string]*types.Named
	packages   map[*types.Package]*packages.Package
}

type mount struct {
	prefix   string
	policies []string
}

type moduleReader struct {
	ex     *exporter
	pkg    *packages.Package
	vars   map[types.Object]ast.Expr
	values map[types.Object]any
	mounts map[types.Object]mount
	root   types.Object
	prefix string
}

func (e *exporter) module(p *packages.Package, prefix string) error {
	var register *ast.FuncDecl
	for _, file := range p.Syntax {
		for _, decl := range file.Decls {
			if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == "Register" && f.Recv == nil {
				register = f
			}
		}
	}
	if register == nil || register.Body == nil || register.Type.Params == nil || len(register.Type.Params.List) == 0 || len(register.Type.Params.List[0].Names) != 1 {
		return fmt.Errorf("%s: expected Register(router *httpx.Router, ...)", p.PkgPath)
	}
	r := &moduleReader{ex: e, pkg: p, vars: map[types.Object]ast.Expr{}, values: map[types.Object]any{}, mounts: map[types.Object]mount{}, root: p.TypesInfo.Defs[register.Type.Params.List[0].Names[0]], prefix: prefix}
	if !isRouter(r.root.Type(), e.httpx) {
		return fmt.Errorf("%s: first Register parameter must be *httpx.Router", p.PkgPath)
	}
	r.mounts[r.root] = mount{prefix: prefix}
	for _, stmt := range register.Body.List {
		if err := r.statement(stmt); err != nil {
			return fmt.Errorf("%s: %w", p.Fset.Position(stmt.Pos()), err)
		}
		if _, ok := stmt.(*ast.ReturnStmt); ok {
			break
		}
	}
	return nil
}

func (r *moduleReader) statement(stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if len(s.Lhs) != len(s.Rhs) {
			return fmt.Errorf("multi-value assignments in Register are unsupported")
		}
		if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
			return fmt.Errorf("compound assignment in Register is unsupported")
		}
		// Evaluate all RHS values against the old environment before assigning.
		updates := make([]func(), 0, len(s.Lhs))
		for i, lhs := range s.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok {
				return fmt.Errorf("Register assignments must target local identifiers")
			}
			update, err := r.assignment(r.pkg.TypesInfo.ObjectOf(id), s.Rhs[i])
			if err != nil {
				return err
			}
			updates = append(updates, update)
		}
		for _, update := range updates {
			update()
		}
		for _, expr := range s.Rhs {
			if r.containsRegistration(expr) {
				return fmt.Errorf("registrations must be direct statements in Register")
			}
		}
	case *ast.DeclStmt:
		decl, ok := s.Decl.(*ast.GenDecl)
		if !ok {
			return fmt.Errorf("unsupported declaration")
		}
		if r.containsRegistration(decl) {
			return fmt.Errorf("registrations must be direct statements in Register")
		}
		for _, spec := range decl.Specs {
			if v, ok := spec.(*ast.ValueSpec); ok {
				for i, id := range v.Names {
					if i < len(v.Values) {
						update, err := r.assignment(r.pkg.TypesInfo.Defs[id], v.Values[i])
						if err != nil {
							return err
						}
						update()
					}
				}
			}
		}
	case *ast.ExprStmt:
		call, ok := s.X.(*ast.CallExpr)
		if !ok {
			return nil
		}
		fn := function(r.pkg.TypesInfo, call.Fun)
		if isTypedFunction(fn, r.ex.httpx) {
			return r.endpoint(call, fn.Name())
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && isRouter(r.pkg.TypesInfo.TypeOf(sel.X), r.ex.httpx) {
			if r.ex.routesOnly {
				if route, ok := r.rawRoute(call, sel); ok {
					r.ex.raw = append(r.ex.raw, route)
					return nil
				}
			}
			r.ex.contract.Skipped = append(r.ex.contract.Skipped, fmt.Sprintf("%s: raw Router.%s at %s:%d", r.pkg.Name, sel.Sel.Name, filepath.Base(r.pkg.Fset.Position(call.Pos()).Filename), r.pkg.Fset.Position(call.Pos()).Line))
			return nil
		}
		if r.containsRegistration(call) {
			return fmt.Errorf("nested or helper route registration is unsupported")
		}
		for _, arg := range call.Args {
			if isRouter(r.pkg.TypesInfo.TypeOf(arg), r.ex.httpx) {
				return fmt.Errorf("registration helper receiving a router is unsupported; declare typed routes directly in Register")
			}
		}
	case *ast.ReturnStmt:
		if len(s.Results) != 0 {
			return fmt.Errorf("Register return values are unsupported")
		}
	default:
		return fmt.Errorf("dynamic Register control flow (%T) is unsupported; declare routes as straight-line statements", stmt)
	}
	return nil
}

func (r *moduleReader) assignment(obj types.Object, expr ast.Expr) (func(), error) {
	if obj == nil {
		return func() {}, nil
	}
	if isRouter(obj.Type(), r.ex.httpx) {
		m, err := r.router(expr, 0)
		if err != nil {
			return nil, err
		}
		m.policies = append([]string(nil), m.policies...)
		return func() { r.mounts[obj] = m }, nil
	}
	value, err := r.value(expr)
	return func() {
		r.vars[obj] = expr
		delete(r.values, obj)
		if err == nil {
			r.values[obj] = value
		}
	}, nil
}

func isTypedFunction(fn *types.Func, pkg string) bool {
	return fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == pkg &&
		fn.Type().(*types.Signature).Recv() == nil && isTyped(fn.Name())
}

func isTyped(name string) bool {
	switch name {
	case "Get", "Delete", "Post", "Put", "Patch", "NoInput":
		return true
	}
	return false
}
func function(info *types.Info, expr ast.Expr) *types.Func {
	switch e := expr.(type) {
	case *ast.IndexExpr:
		return function(info, e.X)
	case *ast.IndexListExpr:
		return function(info, e.X)
	case *ast.SelectorExpr:
		f, _ := info.Uses[e.Sel].(*types.Func)
		return f
	case *ast.Ident:
		f, _ := info.Uses[e].(*types.Func)
		return f
	}
	return nil
}
func isRouter(t types.Type, pkg string) bool {
	if t == nil {
		return false
	}
	ptr, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := types.Unalias(ptr.Elem()).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkg && n.Obj().Name() == "Router"
}
func (r *moduleReader) containsRegistration(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			f := function(r.pkg.TypesInfo, c.Fun)
			if isTypedFunction(f, r.ex.httpx) {
				found = true
			}
		}
		return !found
	})
	return found
}

func (r *moduleReader) value(expr ast.Expr) (any, error) {
	return staticValue(r.pkg.TypesInfo, expr, r.vars, r.values, 0)
}
func (r *moduleReader) str(expr ast.Expr) (string, error) {
	v, err := r.value(expr)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("expected a static string")
	}
	return s, nil
}

func (r *moduleReader) router(expr ast.Expr, depth int) (mount, error) {
	if depth > 32 {
		return mount{}, fmt.Errorf("router derivation too deep")
	}
	if id, ok := expr.(*ast.Ident); ok {
		obj := r.pkg.TypesInfo.ObjectOf(id)
		if m, ok := r.mounts[obj]; ok {
			m.policies = append([]string(nil), m.policies...)
			return m, nil
		}
		if value, ok := r.vars[obj]; ok {
			return r.router(value, depth+1)
		}
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			m, err := r.router(sel.X, depth+1)
			if err != nil {
				return mount{}, err
			}
			switch sel.Sel.Name {
			case "Group":
				if len(call.Args) == 0 {
					return mount{}, fmt.Errorf("Group needs a static prefix")
				}
				p, err := r.str(call.Args[0])
				if err != nil {
					return mount{}, err
				}
				m.prefix = joinRoute(m.prefix, p)
			case "Require":
				if call.Ellipsis.IsValid() {
					return mount{}, fmt.Errorf("dynamic Require policies are unsupported")
				}
				for _, a := range call.Args {
					name, err := r.str(a)
					if err != nil {
						return mount{}, err
					}
					m.policies = append(m.policies, name)
				}
			default:
				return mount{}, fmt.Errorf("unsupported router derivation %s", sel.Sel.Name)
			}
			return m, nil
		}
	}
	return mount{}, fmt.Errorf("router must be the Register parameter or a statically derived Group/Require")
}

func (r *moduleReader) endpoint(call *ast.CallExpr, verb string) error {
	if call.Ellipsis.IsValid() {
		return fmt.Errorf("dynamic endpoint options are unsupported")
	}
	pathIndex, handlerIndex := 1, 2
	op := Operation{Module: r.pkg.Name, Status: 200, Binding: "query", Method: strings.ToUpper(verb)}
	if verb == "NoInput" {
		pathIndex = 2
		handlerIndex = 3
		op.Binding = "none"
		var err error
		op.Method, err = r.str(call.Args[1])
		if err != nil {
			return err
		}
	}
	if verb == "Post" || verb == "Put" || verb == "Patch" {
		op.Binding = "json"
	}
	m, err := r.router(call.Args[0], 0)
	if err != nil {
		return err
	}
	path, err := r.str(call.Args[pathIndex])
	if err != nil {
		return err
	}
	op.Path = joinRoute(m.prefix, path)
	op.Policies = append(op.Policies, m.policies...)
	additional := []string{}
	open := false
	for _, arg := range call.Args[handlerIndex+1:] {
		opt, ok := arg.(*ast.CallExpr)
		if !ok {
			return fmt.Errorf("endpoint options must be direct httpx option calls")
		}
		fn := function(r.pkg.TypesInfo, opt.Fun)
		if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != r.ex.httpx || opt.Ellipsis.IsValid() {
			return fmt.Errorf("custom/dynamic endpoint options are unsupported")
		}
		switch fn.Name() {
		case "Operation":
			op.Name, err = r.str(opt.Args[0])
		case "WithStatus":
			v, e := r.value(opt.Args[0])
			err = e
			if err == nil {
				n, ok := v.(int64)
				if !ok {
					return fmt.Errorf("status must be a constant integer")
				}
				op.Status = int(n)
			}
		case "WithNoContent":
			op.NoContent = true
		case "WithMaxBytes": // Server limit; client preserves ordinary JSON transport.
		case "Open":
			open = true
		case "Policy":
			for _, a := range opt.Args {
				name, e := r.str(a)
				if e != nil {
					return e
				}
				additional = append(additional, name)
			}
		default:
			return fmt.Errorf("unsupported endpoint option %s", fn.Name())
		}
		if err != nil {
			return err
		}
	}
	if open {
		op.Policies = nil
	}
	op.Policies = append(op.Policies, additional...)
	seen := map[string]bool{}
	unique := []string{}
	for _, p := range op.Policies {
		if !seen[p] {
			unique = append(unique, p)
			seen[p] = true
		}
	}
	op.Policies = unique
	if op.NoContent && op.Status == 200 {
		op.Status = 204
	}
	if op.Status == 204 || op.Status == 304 || op.Method == "HEAD" {
		op.NoContent = true
	}
	if r.ex.routesOnly {
		r.ex.contract.Operations = append(r.ex.contract.Operations, op)
		return nil
	}
	if op.Name == "" {
		h := call.Args[handlerIndex]
		if c, ok := h.(*ast.CallExpr); ok {
			h = c.Fun
		}
		fn := function(r.pkg.TypesInfo, h)
		if fn == nil {
			return fmt.Errorf("anonymous handlers need httpx.Operation(\"stable_name\")")
		}
		op.Name = r.pkg.Name + "_" + strings.TrimPrefix(fn.Name(), "handle")
	}
	ht := r.pkg.TypesInfo.TypeOf(call.Args[handlerIndex])
	sig, ok := ht.Underlying().(*types.Signature)
	if !ok || sig.Results().Len() != 2 {
		return fmt.Errorf("handler must return (response, error)")
	}
	if op.Binding != "none" {
		if sig.Params().Len() != 2 {
			return fmt.Errorf("handler must accept (context.Context, request)")
		}
		t, err := r.ex.typeOf(sig.Params().At(1).Type(), 0)
		if err != nil {
			return fmt.Errorf("%s request: %w", op.Name, err)
		}
		op.Request = &t
	}
	if !op.NoContent {
		t, err := r.ex.typeOf(sig.Results().At(0).Type(), 0)
		if err != nil {
			return fmt.Errorf("%s response: %w", op.Name, err)
		}
		op.Response = &t
	}
	r.ex.contract.Operations = append(r.ex.contract.Operations, op)
	return nil
}

func joinRoute(prefix, path string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" && !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if path == "" {
		if prefix == "" {
			return "/"
		}
		return prefix
	}
	return prefix + "/" + strings.TrimPrefix(path, "/")
}

func (e *exporter) typeOf(t types.Type, depth int) (Type, error) {
	if depth > 64 {
		return Type{}, fmt.Errorf("DTO nesting exceeds 64 levels")
	}
	t = types.Unalias(t)
	if n, ok := t.(*types.Named); ok {
		if n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "time" && n.Obj().Name() == "Time" {
			return Type{Kind: "time"}, nil
		}
		// Newer Go releases alias json.RawMessage to jsontext.Value.
		if n.Obj().Pkg() != nil && ((n.Obj().Pkg().Path() == "encoding/json" && n.Obj().Name() == "RawMessage") || (n.Obj().Pkg().Path() == "encoding/json/jsontext" && n.Obj().Name() == "Value")) {
			return Type{Kind: "raw"}, nil
		}
		hasCodec := false
		for _, mt := range []types.Type{n, types.NewPointer(n)} {
			set := types.NewMethodSet(mt)
			for i := 0; i < set.Len(); i++ {
				switch set.At(i).Obj().Name() {
				case "MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText":
					hasCodec = true
				}
			}
		}
		if n.TypeArgs() != nil && n.TypeArgs().Len() > 0 {
			return Type{}, fmt.Errorf("generic DTO %s is unsupported in v1", n)
		}
		if name, ok := e.named[n]; ok {
			return Type{Kind: "ref", Name: name}, nil
		}
		pkg := ""
		if n.Obj().Pkg() != nil {
			pkg = n.Obj().Pkg().Name()
		}
		name := GoName(pkg + "_" + n.Obj().Name())
		if reservedTypes[name] {
			name += "DTO"
		}
		if previous, ok := e.owners[name]; ok && previous != n {
			return Type{}, fmt.Errorf("DTO name collision %s; use distinct package/type names", name)
		}
		e.named[n] = name
		e.owners[name] = n
		var underlying Type
		var err error
		if hasCodec {
			underlying, err = e.weldEnum(n)
		} else {
			underlying, err = e.typeOf(n.Underlying(), depth+1)
		}
		if err != nil {
			return Type{}, err
		}
		e.defs[name] = Definition{Name: name, Type: underlying}
		return Type{Kind: "ref", Name: name}, nil
	}
	switch t := t.(type) {
	case *types.Basic:
		name := t.Name()
		if name == "byte" {
			name = "uint8"
		}
		if name == "rune" {
			name = "int32"
		}
		if !basicTypes[name] {
			return Type{}, fmt.Errorf("unsupported JSON scalar %s", t)
		}
		return Type{Kind: "basic", Name: name}, nil
	case *types.Pointer:
		elem, err := e.typeOf(t.Elem(), depth+1)
		return Type{Kind: "pointer", Elem: &elem}, err
	case *types.Slice:
		elem, err := e.typeOf(t.Elem(), depth+1)
		return Type{Kind: "slice", Elem: &elem}, err
	case *types.Array:
		elem, err := e.typeOf(t.Elem(), depth+1)
		return Type{Kind: "array", Elem: &elem, Len: t.Len()}, err
	case *types.Map:
		key, err := e.typeOf(t.Key(), depth+1)
		if err != nil {
			return Type{}, fmt.Errorf("map key: %w", err)
		}
		elem, err := e.typeOf(t.Elem(), depth+1)
		return Type{Kind: "map", Key: &key, Elem: &elem}, err
	case *types.Struct:
		out := Type{Kind: "struct"}
		for i := 0; i < t.NumFields(); i++ {
			f := t.Field(i)
			if !f.Exported() {
				if f.Embedded() {
					return Type{}, fmt.Errorf("unexported embedded DTO field %s is unsupported; export the embedded type", f.Name())
				}
				continue
			}
			tag := t.Tag(i)
			if !f.Embedded() && reflect.StructTag(tag).Get("json") == "-" && reflect.StructTag(tag).Get("path") == "" && reflect.StructTag(tag).Get("query") == "" {
				continue
			}
			ft, err := e.typeOf(f.Type(), depth+1)
			if err != nil {
				return Type{}, fmt.Errorf("field %s: %w", f.Name(), err)
			}
			fieldName := f.Name()
			if f.Embedded() {
				base := ft
				if base.Kind == "pointer" && base.Elem != nil {
					base = *base.Elem
				}
				if base.Kind != "ref" {
					return Type{}, fmt.Errorf("embedded special-codec field %s is unsupported", f.Name())
				}
				fieldName = base.Name
				under := types.Unalias(f.Type())
				if ptr, ok := under.(*types.Pointer); ok {
					under = ptr.Elem()
				}
				if _, ok := under.Underlying().(*types.Struct); !ok {
					jt := reflect.StructTag(tag).Get("json")
					if strings.Split(jt, ",")[0] == "" {
						replacement := "json:" + strconv.Quote(f.Name()+jt)
						original := "json:" + strconv.Quote(jt)
						if strings.Contains(tag, original) {
							tag = strings.Replace(tag, original, replacement, 1)
						} else {
							tag = replacement + " " + tag
						}
					}
				}
			}
			out.Fields = append(out.Fields, Field{Name: fieldName, Type: ft, Tag: tag, Embedded: f.Embedded()})
		}
		return out, nil
	default:
		return Type{}, fmt.Errorf("unsupported JSON DTO type %s", t)
	}
}

// staticValue deliberately handles a small expression language. It never
// executes Go; dynamic mounts/options fail rather than producing wrong URLs.
func staticValue(info *types.Info, expr ast.Expr, vars map[types.Object]ast.Expr, env map[types.Object]any, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("static expression too deep")
	}
	if tv, ok := info.Types[expr]; ok && tv.Value != nil {
		switch tv.Value.Kind() {
		case constant.String:
			return constant.StringVal(tv.Value), nil
		case constant.Int:
			if n, ok := constant.Int64Val(tv.Value); ok {
				return n, nil
			}
		case constant.Bool:
			return constant.BoolVal(tv.Value), nil
		}
	}
	switch x := expr.(type) {
	case *ast.ParenExpr:
		return staticValue(info, x.X, vars, env, depth+1)
	case *ast.Ident:
		obj := info.ObjectOf(x)
		if v, ok := env[obj]; ok {
			return v, nil
		}
		if e, ok := vars[obj]; ok {
			return staticValue(info, e, vars, env, depth+1)
		}
	case *ast.BinaryExpr:
		a, err := staticValue(info, x.X, vars, env, depth+1)
		if err != nil {
			return nil, err
		}
		b, err := staticValue(info, x.Y, vars, env, depth+1)
		if err != nil {
			return nil, err
		}
		switch x.Op {
		case token.ADD:
			as, aok := a.(string)
			bs, bok := b.(string)
			if aok && bok {
				return as + bs, nil
			}
		case token.EQL:
			return a == b, nil
		case token.NEQ:
			return a != b, nil
		}
	}
	return nil, fmt.Errorf("expression is not statically evaluable")
}

func modulePrefix(p *packages.Package, name string) (string, error) {
	for _, file := range p.Syntax {
		for _, decl := range file.Decls {
			f, ok := decl.(*ast.FuncDecl)
			if !ok || f.Name.Name != "ModulePrefix" || f.Recv != nil {
				continue
			}
			if f.Body == nil || len(f.Type.Params.List) != 1 || len(f.Type.Params.List[0].Names) != 1 {
				return "", fmt.Errorf("unsupported ModulePrefix signature")
			}
			env := map[types.Object]any{p.TypesInfo.Defs[f.Type.Params.List[0].Names[0]]: name}
			v, done, err := staticStatements(p.TypesInfo, f.Body.List, env)
			if err != nil {
				return "", err
			}
			if !done {
				return "", fmt.Errorf("ModulePrefix has no static return")
			}
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("ModulePrefix must return a string")
			}
			return s, nil
		}
	}
	return "", fmt.Errorf("ModulePrefix not found")
}

func staticStatements(info *types.Info, list []ast.Stmt, env map[types.Object]any) (any, bool, error) {
	for _, stmt := range list {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if len(s.Results) != 1 {
				return nil, false, fmt.Errorf("unsupported static return")
			}
			v, err := staticValue(info, s.Results[0], nil, env, 0)
			return v, true, err
		case *ast.SwitchStmt:
			if s.Init != nil || s.Tag == nil {
				return nil, false, fmt.Errorf("unsupported ModulePrefix switch")
			}
			v, err := staticValue(info, s.Tag, nil, env, 0)
			if err != nil {
				return nil, false, err
			}
			var fallback []ast.Stmt
			for _, stmt := range s.Body.List {
				c := stmt.(*ast.CaseClause)
				if c.List == nil {
					fallback = c.Body
					continue
				}
				for _, expr := range c.List {
					cv, err := staticValue(info, expr, nil, env, 0)
					if err != nil {
						return nil, false, err
					}
					if v == cv {
						return staticStatements(info, c.Body, env)
					}
				}
			}
			if fallback != nil {
				return staticStatements(info, fallback, env)
			}
		case *ast.IfStmt:
			if s.Init != nil {
				return nil, false, fmt.Errorf("unsupported ModulePrefix if initializer")
			}
			v, err := staticValue(info, s.Cond, nil, env, 0)
			if err != nil {
				return nil, false, err
			}
			b, ok := v.(bool)
			if !ok {
				return nil, false, fmt.Errorf("static condition must be boolean")
			}
			if b {
				v, done, err := staticStatements(info, s.Body.List, env)
				if err != nil || done {
					return v, done, err
				}
			} else if s.Else != nil {
				v, done, err := staticStatements(info, []ast.Stmt{s.Else}, env)
				if err != nil || done {
					return v, done, err
				}
			}
		case *ast.BlockStmt:
			v, done, err := staticStatements(info, s.List, env)
			if err != nil || done {
				return v, done, err
			}
		default:
			return nil, false, fmt.Errorf("dynamic ModulePrefix statement %T is unsupported", stmt)
		}
	}
	return nil, false, nil
}
