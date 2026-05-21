package pytest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"

	"github.com/teo-dev/teo/internal/model"
)

// astSigScript is a self-contained Python program that emits, as JSON, a
// normalized signature of each test function's BODY for the files passed on
// argv. Using the stdlib `ast` module makes the signature stable across
// reformatting and comment edits (neither is in the AST) while changing when the
// test's logic changes. Body-only (excludes the def name/args/decorators) so it
// mirrors the Go adapter's go/ast signature. S-06-01 / T-06-01-03.
//
// Output shape: { "<argv path>": { "<qualname>": "<16-hex sig>", ... }, ... }
// where qualname is "test_foo" for a module function and "TestBar::test_baz"
// for a method on a Test* class (matching pytest's nodeid suffix).
const astSigScript = `import ast, sys, json, hashlib

def body_sig(node):
    parts = [ast.dump(s, annotate_fields=False) for s in node.body]
    return hashlib.sha256("".join(parts).encode("utf-8")).hexdigest()[:16]

FUNC = (ast.FunctionDef, ast.AsyncFunctionDef)
out = {}
for path in sys.argv[1:]:
    fns = {}
    try:
        with open(path, "r", encoding="utf-8") as f:
            tree = ast.parse(f.read())
        for node in tree.body:
            if isinstance(node, FUNC) and node.name.startswith("test"):
                fns[node.name] = body_sig(node)
            elif isinstance(node, ast.ClassDef) and node.name.startswith("Test"):
                for sub in node.body:
                    if isinstance(sub, FUNC) and sub.name.startswith("test"):
                        fns[node.name + "::" + sub.name] = body_sig(sub)
    except Exception:
        pass
    out[path] = fns
json.dump(out, sys.stdout)
`

// astSignatures runs the embedded Python helper over the given files (relative
// to workdir) and returns map[path]map[qualname]signature. Best-effort: if
// Python is unavailable or the helper fails, it returns nil and Discover
// proceeds with empty signatures.
func (a *Adapter) astSignatures(ctx context.Context, workdir string, paths []string) map[string]map[string]string {
	if len(paths) == 0 {
		return nil
	}
	scriptFile, err := os.CreateTemp("", "teo-pyast-*.py")
	if err != nil {
		return nil
	}
	defer func() { _ = os.Remove(scriptFile.Name()) }()
	if _, err := scriptFile.WriteString(astSigScript); err != nil {
		_ = scriptFile.Close()
		return nil
	}
	_ = scriptFile.Close()

	args := append([]string{scriptFile.Name()}, paths...)
	for _, py := range a.pyBins() {
		// #nosec G204 -- caller-supplied test paths; same SPI exemption the rest
		// of this adapter relies on (see docs/adapters/spi.md).
		cmd := exec.CommandContext(ctx, py, args...)
		cmd.Dir = workdir
		out, err := cmd.Output()
		if err != nil {
			continue // try the next interpreter name
		}
		var res map[string]map[string]string
		if err := json.Unmarshal(out, &res); err != nil {
			return nil
		}
		return res
	}
	return nil
}

// pyBins returns the interpreter names to try, in order.
func (a *Adapter) pyBins() []string {
	if a.PyBin != "" {
		return []string{a.PyBin}
	}
	return []string{"python3", "python"}
}

// distinctPaths returns the unique Path values across entries, preserving order.
func distinctPaths(entries []model.TestEntry) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if _, ok := seen[e.Path]; ok {
			continue
		}
		seen[e.Path] = struct{}{}
		out = append(out, e.Path)
	}
	return out
}
