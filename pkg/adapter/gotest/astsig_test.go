package gotest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// firstFunc parses src and returns the named top-level function declaration.
func firstFunc(t *testing.T, src, name string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x_test.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("func %s not found", name)
	return nil
}

func sigOf(t *testing.T, src, name string) string {
	return goASTSignature(firstFunc(t, src, name))
}

// Reformatting and comment edits must NOT change the signature — they're not in
// the AST node tree.
func TestGoASTSignatureStableAcrossFormatting(t *testing.T) {
	a := `package p
import "testing"
func TestFoo(t *testing.T) {
	x := 1
	if x == 1 { t.Log("ok") }
}`
	b := `package p

import "testing"

// a leading comment
func TestFoo(t *testing.T) {
	x := 1 // trailing comment

	if x == 1 {
		t.Log("ok")
	}
}`
	if sigOf(t, a, "TestFoo") != sigOf(t, b, "TestFoo") {
		t.Fatal("signature changed across pure formatting/comment edits")
	}
	if sigOf(t, a, "TestFoo") == "" {
		t.Fatal("signature should be non-empty for a non-empty body")
	}
}

// A genuine logic change must change the signature.
func TestGoASTSignatureChangesWithLogic(t *testing.T) {
	a := `package p
import "testing"
func TestFoo(t *testing.T) { t.Log("a") }`
	b := `package p
import "testing"
func TestFoo(t *testing.T) { t.Error("a") }` // Log -> Error
	if sigOf(t, a, "TestFoo") == sigOf(t, b, "TestFoo") {
		t.Fatal("signature should differ when the call changes")
	}
}

// A changed string literal must change the signature.
func TestGoASTSignatureChangesWithLiteral(t *testing.T) {
	a := `package p
import "testing"
func TestFoo(t *testing.T) { t.Log("a") }`
	b := `package p
import "testing"
func TestFoo(t *testing.T) { t.Log("b") }`
	if sigOf(t, a, "TestFoo") == sigOf(t, b, "TestFoo") {
		t.Fatal("signature should differ when a literal changes")
	}
}

// Renaming a local variable changes the signature (idents are part of the body).
func TestGoASTSignatureSensitiveToIdent(t *testing.T) {
	a := `package p
import "testing"
func TestFoo(t *testing.T) { x := 1; _ = x }`
	b := `package p
import "testing"
func TestFoo(t *testing.T) { y := 1; _ = y }`
	if sigOf(t, a, "TestFoo") == sigOf(t, b, "TestFoo") {
		t.Fatal("signature should track identifier names")
	}
}

// A bodyless / nil function yields an empty signature (no panic).
func TestGoASTSignatureEmpty(t *testing.T) {
	if goASTSignature(nil) != "" {
		t.Fatal("nil func should produce empty signature")
	}
	fn := firstFunc(t, "package p\nfunc Bar()", "Bar")
	if goASTSignature(fn) != "" {
		t.Fatal("bodyless func should produce empty signature")
	}
}
