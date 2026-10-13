package httpcontract

import (
	"bytes"
	"fmt"
	"go/constant"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xwudao/weld/internal/genenum"
)

func enumSymbols(d Definition) []string {
	out := []string{d.Name + "Values", d.Name + "Names", "Parse" + d.Name}
	for _, v := range d.Type.Values {
		out = append(out, v.Name)
	}
	return out
}

// Recognize the actual weld generator output, not just a marker or a matching
// method name. Modified/custom codecs must not silently acquire different wire
// behavior in the client. No source code is ever executed.
func (e *exporter) weldEnum(n *types.Named) (Type, error) {
	unsupported := fmt.Errorf("%s has custom serialization that is not an unchanged weld enum codec", n)
	basic, ok := n.Underlying().(*types.Basic)
	if !ok || basic.Kind() != types.String {
		return Type{}, unsupported
	}
	p := e.packages[n.Obj().Pkg()]
	if p == nil {
		return Type{}, unsupported
	}
	method, _, _ := types.LookupFieldOrMethod(n, false, n.Obj().Pkg(), "MarshalText")
	if method == nil {
		return Type{}, unsupported
	}
	path := p.Fset.Position(method.Pos()).Filename
	pkg, err := genenum.LoadDir(filepath.Dir(path))
	if err != nil {
		return Type{}, err
	}
	if pkg == nil {
		return Type{}, unsupported
	}
	for _, item := range pkg.Enums {
		if item.Name != n.Obj().Name() {
			continue
		}
		want, err := genenum.Render(pkg, item)
		if err != nil {
			return Type{}, err
		}
		actual, err := os.ReadFile(filepath.Join(filepath.Dir(path), genenum.FileName(item)))
		if err != nil || !bytes.Equal(actual, want) {
			return Type{}, unsupported
		}
		// Every codec method must originate in that exact generated file; a
		// hand-written JSON method takes precedence over MarshalText in Go.
		for _, t := range []types.Type{n, types.NewPointer(n)} {
			set := types.NewMethodSet(t)
			for i := 0; i < set.Len(); i++ {
				m := set.At(i).Obj()
				switch m.Name() {
				case "MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText":
					if p.Fset.Position(m.Pos()).Filename != filepath.Join(filepath.Dir(path), genenum.FileName(item)) {
						return Type{}, unsupported
					}
				}
			}
		}
		out := Type{Kind: "enum"}
		for _, v := range item.Values {
			c, ok := n.Obj().Pkg().Scope().Lookup(v.Const).(*types.Const)
			if !ok || !types.Identical(c.Type(), n) || c.Val().Kind() != constant.String {
				return Type{}, unsupported
			}
			out.Values = append(out.Values, EnumValue{Name: GoName(n.Obj().Pkg().Name() + "_" + v.Const), Value: constant.StringVal(c.Val())})
		}
		return out, nil
	}
	return Type{}, unsupported
}

func enumFileName(d Definition) string { return "enum_" + genenum.FileName(genenum.Enum{Name: d.Name}) }

func renderEnum(d Definition, packageName string) ([]byte, error) {
	item := genenum.Enum{Name: d.Name, File: "types_gen.go"}
	for _, v := range d.Type.Values {
		item.Values = append(item.Values, genenum.Value{Const: v.Name, Raw: v.Value})
	}
	data, err := genenum.Render(&genenum.Package{Name: packageName}, item)
	if err != nil {
		return nil, err
	}
	// These methods are client-owned, not files maintained by weld gen enum.
	body := string(data)
	body = body[strings.Index(body, "package "):]
	return []byte(ClientHeader + "\n\n" + body), nil
}
