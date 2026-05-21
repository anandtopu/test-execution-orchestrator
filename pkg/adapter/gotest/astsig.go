package gotest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// astSignatures parses every package's test files once and returns a map keyed
// by "<importPath>::<TestFuncName>" → a normalized signature of that function's
// body (S-14-01 AC4). It is best-effort: any failure (no module, parse error)
// yields a partial or empty map, and Discover degrades to an empty AST sig
// rather than failing.
func (a *Adapter) astSignatures(ctx context.Context, workdir string) map[string]string {
	// One `go list` describes each package's directory and its test files.
	// Fields are tab-separated; file lists are comma-separated.
	const tmpl = "{{.ImportPath}}\t{{.Dir}}\t{{join .TestGoFiles \",\"}}\t{{join .XTestGoFiles \",\"}}"
	cmd := exec.CommandContext(ctx, a.bin(), "list", "-f", tmpl, "./...")
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	sigs := map[string]string{}
	fset := token.NewFileSet()
	// Split per-line and trim only \r — never TrimSpace the whole output, which
	// would eat the trailing tab of an empty last field (XTestGoFiles) and
	// collapse the 4 columns to 3.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			continue
		}
		importPath, dir := fields[0], fields[1]
		var files []string
		files = append(files, splitList(fields[2])...)
		files = append(files, splitList(fields[3])...)
		for _, f := range files {
			parseGoTestFile(fset, importPath, filepath.Join(dir, f), sigs)
		}
	}
	return sigs
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// parseGoTestFile adds a signature for each top-level test/benchmark/example
// function in the file to sigs, keyed by "<importPath>::<name>".
func parseGoTestFile(fset *token.FileSet, importPath, path string, sigs map[string]string) {
	file, err := parser.ParseFile(fset, path, nil, 0) // mode 0: no comments in the tree
	if err != nil {
		return
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil { // tests are top-level funcs, never methods
			continue
		}
		n := fn.Name.Name
		if !strings.HasPrefix(n, "Test") && !strings.HasPrefix(n, "Benchmark") && !strings.HasPrefix(n, "Example") {
			continue
		}
		sigs[importPath+"::"+n] = goASTSignature(fn)
	}
}

// goASTSignature hashes the structure + identifiers + literals of a function's
// body. It is stable across reformatting and comment edits (those aren't in the
// AST node tree) but changes when the test's logic changes. Returns "" for an
// empty/bodyless function.
func goASTSignature(fn *ast.FuncDecl) string {
	if fn == nil || fn.Body == nil {
		return ""
	}
	h := sha256.New()
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case nil:
			return false
		case *ast.Ident:
			_, _ = io.WriteString(h, "id:"+v.Name+";")
		case *ast.BasicLit:
			_, _ = io.WriteString(h, "lit:"+v.Kind.String()+":"+v.Value+";")
		default:
			_, _ = io.WriteString(h, fmt.Sprintf("%T;", n))
		}
		return true
	})
	return hex.EncodeToString(h.Sum(nil))[:16]
}
