package api

import (
	"bytes"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
)

// The public surface of this module is package api and nothing else -- every
// other package is under internal/ and the compiler refuses the import. That
// makes the compatibility promise boundary-checkable, but only if someone
// notices when the list changes. This test is that someone.
//
// It pins two things, and the split matters:
//
//  1. Every exported identifier declared in api/*.go, with its signature. This
//     catches removal, rename and re-signing -- the changes that stop an
//     embedder's code from compiling.
//  2. The method set of every interface an embedder implements or fakes. A
//     method added to Handler or Plugin is source-compatible for code that
//     only *calls* the framework, but it breaks every implementation and every
//     mock. Type aliases hide those methods from an AST walk of this package
//     (they live in internal/core), so this half uses reflection.
//
// Concrete types returned by constructors are deliberately *not* pinned:
// gaining a method on *TCPServer never breaks anyone. Interfaces the framework
// alone implements (SessionManager, Server) are not pinned either -- you
// receive those, you do not write them. Session is the exception, listed
// because handler unit tests routinely fake it.
var updateSurface = flag.Bool("update-surface", false, "rewrite testdata/surface.txt from the current package")

const surfaceGolden = "testdata/surface.txt"

// surfaceProbe is the type argument used to make Codec and TypedHandler
// instantiable for reflection. It is a type of its own so the rendered
// signature reads "M" after normalisation instead of borrowing a standard type
// and implying the interface only works with that type.
type surfaceProbe struct{}

func TestPublicSurface(t *testing.T) {
	got := append(declaredSurface(t), interfaceSurface()...)
	sort.Strings(got)

	if *updateSurface {
		writeGolden(t, surfaceGolden, got)
		t.Logf("rewrote %s with %d entries", surfaceGolden, len(got))
		return
	}

	want := readGolden(t, surfaceGolden)

	missing, added := diffSurface(want, got)
	if len(missing) == 0 && len(added) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("the exported surface of package api changed:\n")
	for _, line := range missing {
		b.WriteString("  removed or re-signed: " + line + "\n")
	}
	for _, line := range added {
		b.WriteString("  added:                " + line + "\n")
	}
	b.WriteString("\nRemoving or re-signing an exported identifier is a breaking change: it needs an\n" +
		"ADR and a CHANGELOG entry under Breaking. Adding one is free at runtime, but it\n" +
		"is a new promise -- decide which tier it belongs to in\n" +
		"docs/architecture/COMPATIBILITY.md, then regenerate the golden:\n\n" +
		"    go test ./api -run TestPublicSurface -update-surface\n")
	t.Fatal(b.String())
}

// TestErrorSentinelsAreTheSameValues guards the alias, not the name. The whole
// point of api.ErrFrameTooLarge is that errors.Is matches the error the
// framework returned; a copy created with errors.New would keep the doc comment
// and lose the contract, and every test above would still pass.
func TestErrorSentinelsAreTheSameValues(t *testing.T) {
	pairs := []struct {
		name string
		api  error
		core error
	}{
		{"ErrClosed", ErrClosed, core.ErrClosed},
		{"ErrSessionClosed", ErrSessionClosed, core.ErrSessionClosed},
		{"ErrServerClosed", ErrServerClosed, core.ErrServerClosed},
		{"ErrWriteQueueFull", ErrWriteQueueFull, core.ErrWriteQueueFull},
		{"ErrFrameTooLarge", ErrFrameTooLarge, core.ErrFrameTooLarge},
		{"ErrSessionCapacity", ErrSessionCapacity, core.ErrSessionCapacity},
		{"ErrDuplicateSession", ErrDuplicateSession, core.ErrDuplicateSession},
		{"ErrDuplicateProtocol", ErrDuplicateProtocol, core.ErrDuplicateProtocol},
		{"ErrInvalidArgument", ErrInvalidArgument, core.ErrInvalidArgument},
		{"ErrNoServers", ErrNoServers, core.ErrNoServers},
		{"ErrPluginPanic", ErrPluginPanic, core.ErrPluginPanic},
		{"ErrHandlerPanic", ErrHandlerPanic, core.ErrHandlerPanic},
		{"ErrPluginDrop", ErrPluginDrop, core.ErrPluginDrop},
		{"ErrPluginBlock", ErrPluginBlock, core.ErrPluginBlock},
	}
	for _, p := range pairs {
		if p.api == nil {
			t.Fatalf("api.%s is nil", p.name)
		}
		if !errors.Is(p.core, p.api) || !errors.Is(p.api, p.core) {
			t.Fatalf("api.%s is not the framework's own value; errors.Is will not match", p.name)
		}
	}
}

// declaredSurface walks api/*.go and renders every exported declaration.
func declaredSurface(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package dir: %v", err)
	}
	pkg, ok := pkgs["api"]
	if !ok {
		t.Fatal("package api not found next to this test")
	}

	files := make([]string, 0, len(pkg.Files))
	for name := range pkg.Files {
		files = append(files, name)
	}
	sort.Strings(files)

	var out []string
	for _, name := range files {
		for _, decl := range pkg.Files[name].Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// Methods of concrete types are not pinned: see the note above.
				if d.Recv != nil || !d.Name.IsExported() {
					continue
				}
				sig := renderNode(fset, stripParamNames(d.Type))
				out = append(out, "func "+d.Name.Name+strings.TrimPrefix(sig, "func"))

			case *ast.GenDecl:
				keyword := "var"
				switch d.Tok {
				case token.TYPE:
					keyword = "type"
				case token.CONST:
					keyword = "const"
				case token.VAR:
				default:
					continue
				}
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							out = append(out, keyword+" "+renderNode(fset, s))
						}
					case *ast.ValueSpec:
						if anyExported(s.Names) {
							out = append(out, keyword+" "+renderNode(fset, s))
						}
					}
				}
			}
		}
	}
	return out
}

// interfaceSurface renders the method set of each interface an embedder writes.
func interfaceSurface() []string {
	ifaces := []struct {
		name string
		typ  reflect.Type
	}{
		{"Plugin", reflect.TypeOf((*Plugin)(nil)).Elem()},
		{"Codec", reflect.TypeOf((*Codec[surfaceProbe])(nil)).Elem()},
		{"Logger", reflect.TypeOf((*Logger)(nil)).Elem()},
		{"Metrics", reflect.TypeOf((*Metrics)(nil)).Elem()},
		{"Tracer", reflect.TypeOf((*Tracer)(nil)).Elem()},
		{"Framer", reflect.TypeOf((*Framer)(nil)).Elem()},
		{"Store", reflect.TypeOf((*Store)(nil)).Elem()},
		{"Session", reflect.TypeOf((*Session)(nil)).Elem()},
	}

	var out []string
	for _, it := range ifaces {
		if it.typ.Kind() != reflect.Interface {
			panic("api." + it.name + " is no longer an interface; update this test")
		}
		for i := 0; i < it.typ.NumMethod(); i++ {
			m := it.typ.Method(i)
			out = append(out, "iface "+it.name+"."+m.Name+" "+normaliseSignature(m.Type.String()))
		}
	}
	return out
}

// TestAliasesPointAtTheInternalTypes pins that these names are aliases and not
// copies. A copy would carry an identical method set, so every other check in
// this file would still pass -- and then a method added to the real interface
// would never reach embedders.
func TestAliasesPointAtTheInternalTypes(t *testing.T) {
	cases := []struct {
		name     string
		api      reflect.Type
		internal reflect.Type
	}{
		{"Handler", reflect.TypeOf((*Handler)(nil)).Elem(), reflect.TypeOf((*core.Handler)(nil)).Elem()},
		{"Plugin", reflect.TypeOf((*Plugin)(nil)).Elem(), reflect.TypeOf((*core.Plugin)(nil)).Elem()},
		{"Session", reflect.TypeOf((*Session)(nil)).Elem(), reflect.TypeOf((*core.Session)(nil)).Elem()},
		{"Framer", reflect.TypeOf((*Framer)(nil)).Elem(), reflect.TypeOf((*tcp.Framer)(nil)).Elem()},
		{"Store", reflect.TypeOf((*Store)(nil)).Elem(), reflect.TypeOf((*store.Store)(nil)).Elem()},
	}
	for _, c := range cases {
		if c.api != c.internal {
			t.Fatalf("api.%s is a distinct type, not an alias of the internal one; "+
				"embedders would silently stop seeing changes to it", c.name)
		}
	}
}

func anyExported(names []*ast.Ident) bool {
	for _, n := range names {
		if n.IsExported() {
			return true
		}
	}
	return false
}

// stripParamNames drops parameter and result names before rendering.
//
// Renaming a parameter breaks nobody -- it is not part of the call syntax, an
// embedder cannot observe it, and IDE refactors do it silently. A gate that
// fires on it teaches people to run -update without reading the diff, which is
// the one failure mode this test cannot afford. Types are kept, and a
// multi-name group such as (a, b string) is expanded to two entries so the
// arity survives the rewrite.
func stripParamNames(ft *ast.FuncType) *ast.FuncType {
	stripped := *ft
	stripped.Params = expandFields(ft.Params)
	stripped.Results = expandFields(ft.Results)
	return &stripped
}

func expandFields(list *ast.FieldList) *ast.FieldList {
	if list == nil {
		return nil
	}
	fields := make([]*ast.Field, 0, len(list.List))
	for _, f := range list.List {
		count := len(f.Names)
		if count == 0 {
			count = 1
		}
		for i := 0; i < count; i++ {
			fields = append(fields, &ast.Field{Type: f.Type})
		}
	}
	return &ast.FieldList{Opening: list.Opening, List: fields, Closing: list.Closing}
}

// renderNode prints one node on a single line. Collapsing whitespace keeps the
// golden stable when gofmt re-wraps a long signature: the promise is the
// signature, not its layout.
func renderNode(fset *token.FileSet, node any) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return "<unprintable: " + err.Error() + ">"
	}
	return strings.Join(strings.Fields(buf.String()), " ")
}

// normaliseSignature rewrites reflect's rendering into the syntax a reader
// expects, because the golden is meant to be read before it is trusted.
// Longest-prefix first: core.SessionState would otherwise be clipped by the
// core.Session rule into something that no longer looks like a change.
func normaliseSignature(s string) string {
	replacements := []struct{ from, to string }{
		{"api.surfaceProbe", "M"},
		{"[]uint8", "[]byte"},
		{"interface {}", "any"},
		{"core.SessionState", "SessionState"},
		{"core.Session", "Session"},
		{"core.Message", "Message"},
		{"core.Protocol", "Protocol"},
		{"core.Span", "Span"},
	}
	for _, r := range replacements {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	return s
}

// TestDiffSurface covers the comparison itself. It is a multiset diff, not a
// set diff: two identical lines can legitimately appear (an interface method
// named the same as a package function, once a promotion happens), and a
// set-based comparison would silently accept a duplicated entry while the
// golden has one.
func TestDiffSurface(t *testing.T) {
	missing, added := diffSurface(
		[]string{"a", "b", "c", "d"},
		[]string{"a", "c", "d", "d"},
	)
	if len(missing) != 1 || missing[0] != "b" {
		t.Fatalf("missing = %v, want [b]", missing)
	}
	if len(added) != 1 || added[0] != "d" {
		t.Fatalf("added = %v, want [d]", added)
	}

	missing, added = diffSurface([]string{"x", "x"}, []string{"x"})
	if len(missing) != 1 || len(added) != 0 {
		t.Fatalf("duplicate in golden: missing = %v, added = %v", missing, added)
	}
}

func readGolden(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\nregenerate it with:\n\n    go test ./api -run TestPublicSurface -update-surface", path, err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

func writeGolden(t *testing.T, path string, lines []string) {
	t.Helper()

	var b strings.Builder
	b.WriteString("# Package api: the compatibility surface. Do not edit by hand.\n")
	b.WriteString("# Regenerate with: go test ./api -run TestPublicSurface -update-surface\n")
	b.WriteString("#\n")
	b.WriteString("# Declarations are listed as written; interface methods come from reflection\n")
	b.WriteString("# because the aliases put the real method sets in internal/core.\n")
	for _, line := range lines {
		b.WriteString(line + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// diffSurface reports golden-only and code-only entries.
func diffSurface(want, got []string) (missing, added []string) {
	inGot := make(map[string]int, len(got))
	for _, line := range got {
		inGot[line]++
	}
	for _, line := range want {
		if inGot[line] == 0 {
			missing = append(missing, line)
			continue
		}
		inGot[line]--
	}

	inWant := make(map[string]int, len(want))
	for _, line := range want {
		inWant[line]++
	}
	for _, line := range got {
		if inWant[line] == 0 {
			added = append(added, line)
			continue
		}
		inWant[line]--
	}
	return missing, added
}
