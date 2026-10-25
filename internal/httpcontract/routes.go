package httpcontract

import (
	"context"
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"sort"
	"strings"

	"github.com/Xwudao/weld/internal/project"
)

// Route is one HTTP route found by reading a project's source.
type Route struct {
	// Module is the business module that registers the route, or the
	// capability that mounts it (web).
	Module string `json:"module"`
	// Method is the HTTP method, or "*" for a pattern that carries none.
	Method string `json:"method"`
	// Path is the full path pattern, mount prefix included.
	Path string `json:"path"`
	// Operation is the stable httpx.Operation ID, when the route declares one.
	Operation string `json:"operation,omitempty"`
	// Policies are the named policies that guard the route; empty is public.
	Policies []string `json:"policies,omitempty"`
	// Kind is "typed" for an httpx typed helper, "raw" for a Router method and
	// "mount" for a capability's catch-all surface.
	Kind string `json:"kind"`
}

// RouteTable is the statically known HTTP surface of a project.
type RouteTable struct {
	Routes []Route `json:"routes"`
	// Unread lists the registrations or whole modules the reader could not
	// resolve statically, so the table says what it is missing instead of
	// silently omitting it.
	Unread []string `json:"unread,omitempty"`
}

// ListRoutes reads every installed business module's Register function and
// returns the routes it declares, typed and raw, with their mount prefix and
// policies. Like Export it type-checks the source and never calls a
// constructor, so it needs no configuration, database or network service.
//
// It is lenient where Export is strict: a module whose Register cannot be read
// statically is reported in Unread rather than failing the table, and DTO
// shapes are not resolved. Routes registered outside a module's Register (a
// hand-written mount in the composition) are not visible to it.
func ListRoutes(ctx context.Context, dir string) (*RouteTable, error) {
	m, err := project.Load(dir)
	if err != nil {
		return nil, err
	}
	if !m.HasCapability("http") {
		return nil, fmt.Errorf("the http capability is not installed; there are no HTTP routes")
	}
	table := &RouteTable{Routes: []Route{}}
	if m.HasCapability("web") {
		table.Routes = append(table.Routes, Route{Module: "web", Method: "GET", Path: "/", Kind: "mount"})
	}
	if len(m.Modules) == 0 {
		return table, nil
	}
	names := make([]string, 0, len(m.Modules))
	for _, mod := range m.Modules {
		names = append(names, mod.Name)
	}
	pkgs, server, err := loadHTTPSource(ctx, dir, m, names)
	if err != nil {
		return nil, err
	}
	for _, p := range pkgs {
		if p == server {
			continue
		}
		ex := &exporter{routesOnly: true, contract: &Contract{}, httpx: m.Module + "/internal/httpx", defs: map[string]Definition{}, named: map[*types.Named]string{}, owners: map[string]*types.Named{}}
		prefix := ""
		name, ok := p.Types.Scope().Lookup("Name").(*types.Const)
		if !ok || name.Val().Kind() != constant.String {
			table.Unread = append(table.Unread, fmt.Sprintf("%s: no constant Name, so its mount prefix is unknown", p.Name))
			continue
		}
		if prefix, err = modulePrefix(server, constant.StringVal(name.Val())); err != nil {
			table.Unread = append(table.Unread, fmt.Sprintf("%s: mount prefix: %v", p.Name, err))
			continue
		}
		if err := ex.module(p, prefix); err != nil {
			table.Unread = append(table.Unread, fmt.Sprintf("%s: %v", p.Name, err))
			continue
		}
		for _, op := range ex.contract.Operations {
			table.Routes = append(table.Routes, Route{Module: op.Module, Method: op.Method, Path: op.Path, Operation: op.Name, Policies: op.Policies, Kind: "typed"})
		}
		table.Routes = append(table.Routes, ex.raw...)
		table.Unread = append(table.Unread, ex.contract.Skipped...)
	}
	sort.SliceStable(table.Routes, func(i, j int) bool {
		a, b := table.Routes[i], table.Routes[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
	sort.Strings(table.Unread)
	return table, nil
}

// rawRoute reads a raw Router registration (Get/Post/Put/Patch/Delete, Raw,
// Handle and HandleFunc) whose router, method and pattern are static. It
// reports false when any part is dynamic, and the caller records the call as
// unread.
func (r *moduleReader) rawRoute(call *ast.CallExpr, sel *ast.SelectorExpr) (Route, bool) {
	route := Route{Module: r.pkg.Name, Kind: "raw"}
	patternIndex := 0
	switch sel.Sel.Name {
	case "Get", "Post", "Put", "Patch", "Delete":
		route.Method = strings.ToUpper(sel.Sel.Name)
	case "Raw":
		if len(call.Args) < 2 {
			return Route{}, false
		}
		method, err := r.str(call.Args[0])
		if err != nil {
			return Route{}, false
		}
		route.Method = strings.ToUpper(method)
		patternIndex = 1
	case "Handle", "HandleFunc":
		route.Method = "*"
	default:
		return Route{}, false
	}
	if len(call.Args) <= patternIndex+1 || call.Ellipsis.IsValid() {
		return Route{}, false
	}
	m, err := r.router(sel.X, 0)
	if err != nil {
		return Route{}, false
	}
	pattern, err := r.str(call.Args[patternIndex])
	if err != nil {
		return Route{}, false
	}
	// A Handle pattern may carry its own method, as net/http patterns do.
	if method, path, ok := strings.Cut(pattern, " "); ok && route.Method == "*" && !strings.HasPrefix(method, "/") {
		route.Method = strings.ToUpper(method)
		pattern = strings.TrimSpace(path)
	}
	route.Path = joinRoute(m.prefix, pattern)
	policies := append([]string(nil), m.policies...)
	var additional []string
	for _, arg := range call.Args[patternIndex+2:] {
		opt, ok := arg.(*ast.CallExpr)
		if !ok {
			return Route{}, false
		}
		fn := function(r.pkg.TypesInfo, opt.Fun)
		if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != r.ex.httpx || opt.Ellipsis.IsValid() {
			return Route{}, false
		}
		switch fn.Name() {
		case "Operation":
			if len(opt.Args) != 1 {
				return Route{}, false
			}
			if route.Operation, err = r.str(opt.Args[0]); err != nil {
				return Route{}, false
			}
		case "Open":
			policies = nil
		case "Policy":
			for _, a := range opt.Args {
				name, err := r.str(a)
				if err != nil {
					return Route{}, false
				}
				additional = append(additional, name)
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range append(policies, additional...) {
		if !seen[name] {
			route.Policies = append(route.Policies, name)
			seen[name] = true
		}
	}
	return route, true
}
