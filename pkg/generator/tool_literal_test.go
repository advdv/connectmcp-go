// Copyright 2025 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package generator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime"
)

// TestToolLiteralRoundTrip parses the emitted literal as Go source and checks
// every field decodes back to the input. It also pins the type qualifier to
// "json.RawMessage" so the literal agrees with the template's "encoding/json"
// import regardless of how the toolchain implements json.RawMessage (it is an
// alias of jsontext.Value under JSON v2).
func TestToolLiteralRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		tool runtime.Tool
	}{
		{
			name: "plain schemas",
			tool: runtime.Tool{
				Name:            "svc_Method",
				Description:     "multi\nline \"quoted\" description",
				RawInputSchema:  []byte(`{"type":"object","properties":{"a":{"type":"string"}}}`),
				RawOutputSchema: []byte(`{"type":"object"}`),
			},
		},
		{
			name: "schema containing a backquote falls back to quoted string",
			tool: runtime.Tool{
				Name:            "svc_Backquote",
				RawInputSchema:  []byte("{\"description\":\"use `x`\"}"),
				RawOutputSchema: []byte(`{}`),
			},
		},
		{
			name: "nil schemas",
			tool: runtime.Tool{Name: "svc_Nil"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			src := toolLiteral(tt.tool)
			expr, err := parser.ParseExpr(src)
			g.Expect(err).NotTo(HaveOccurred(), src)

			lit, ok := expr.(*ast.CompositeLit)
			g.Expect(ok).To(BeTrue(), src)
			g.Expect(exprString(lit.Type)).To(Equal("runtime.Tool"))

			got := runtime.Tool{}
			for _, elt := range lit.Elts {
				kv := elt.(*ast.KeyValueExpr)
				key := kv.Key.(*ast.Ident).Name
				switch key {
				case "Name":
					got.Name = unquote(g, kv.Value)
				case "Description":
					got.Description = unquote(g, kv.Value)
				case "RawInputSchema", "RawOutputSchema":
					call, ok := kv.Value.(*ast.CallExpr)
					g.Expect(ok).To(BeTrue(), "%s should be a conversion", key)
					g.Expect(exprString(call.Fun)).To(Equal("json.RawMessage"))
					g.Expect(call.Args).To(HaveLen(1))
					var raw []byte
					if id, isIdent := call.Args[0].(*ast.Ident); !isIdent || id.Name != "nil" {
						raw = []byte(unquote(g, call.Args[0]))
					}
					reflect.ValueOf(&got).Elem().FieldByName(key).SetBytes(raw)
				default:
					t.Fatalf("unexpected field %q", key)
				}
			}
			g.Expect(got).To(Equal(tt.tool))
		})
	}
}

// TestToolLiteralCoversAllFields fails when runtime.Tool gains a field that
// toolLiteral does not emit, which would silently drop it from generated code.
func TestToolLiteralCoversAllFields(t *testing.T) {
	g := NewWithT(t)

	expr, err := parser.ParseExpr(toolLiteral(runtime.Tool{}))
	g.Expect(err).NotTo(HaveOccurred())

	var emitted []string
	for _, elt := range expr.(*ast.CompositeLit).Elts {
		emitted = append(emitted, elt.(*ast.KeyValueExpr).Key.(*ast.Ident).Name)
	}

	var fields []string
	for f := range reflect.TypeFor[runtime.Tool]().Fields() {
		fields = append(fields, f.Name)
	}
	g.Expect(emitted).To(Equal(fields))
}

func exprString(e ast.Expr) string {
	if sel, ok := e.(*ast.SelectorExpr); ok {
		return exprString(sel.X) + "." + sel.Sel.Name
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func unquote(g *WithT, e ast.Expr) string {
	bl, ok := e.(*ast.BasicLit)
	g.Expect(ok).To(BeTrue())
	g.Expect(bl.Kind).To(Equal(token.STRING))
	s, err := strconv.Unquote(bl.Value)
	g.Expect(err).NotTo(HaveOccurred())
	return s
}
