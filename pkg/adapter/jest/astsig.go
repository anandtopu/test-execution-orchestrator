package jest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"

	"github.com/teo-dev/teo/internal/model"
)

// astSigScript is a self-contained Node program that emits, as JSON, a
// normalized signature of each Jest test block's BODY for the files passed on
// argv. It parses with `@babel/parser` (resolved from the project under test —
// it's a transitive dependency of `babel-jest`, which vanilla `jest` installs by
// default, so it's present in virtually every Jest project) and hashes the
// structure + identifiers + literals of each `it()`/`test()` callback body. Like
// the gotest/pytest AST signatures (S-14-01 / S-06-01) this is stable across
// reformatting and comment edits (neither is in the AST) but changes when the
// test's logic changes. S-14-02 AC3 (v1.5).
//
// Output shape: { "<argv path>": { "<describe > ... > title>": "<16-hex sig>" } }
// where the key matches exactly the per-test Name that parseReport builds from a
// Jest report — the ancestor describe titles joined to the test title with
// " > ". Dynamic titles (it.each, computed/template-interpolated names) are
// skipped: their report Name can't be predicted from the source, so they carry
// an empty signature (path+name+params identity, same as v1.0).
//
// If `@babel/parser` can't be resolved the script prints "{}" and exits 0 (an
// empty, non-error result); the adapter then proceeds with empty signatures.
const astSigScript = `'use strict';
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const resolvePaths = [process.cwd()];
const extra = process.env.TEO_JS_PARSER_PATHS;
if (extra) for (const p of extra.split(path.delimiter)) if (p) resolvePaths.push(p);

let parser;
try {
  parser = require(require.resolve('@babel/parser', { paths: resolvePaths }));
} catch (e) {
  process.stdout.write('{}');
  process.exit(0);
}

const TEST_FNS = new Set(['it', 'test', 'fit', 'xit', 'xtest']);
const DESCRIBE_FNS = new Set(['describe', 'xdescribe', 'fdescribe']);
const SKIP_KEYS = new Set(['loc', 'start', 'end', 'range', 'leadingComments', 'trailingComments', 'innerComments', 'comments', 'tokens']);

// calleeName returns the base identifier of a call: it(...) -> "it",
// it.only(...) -> "it". Returns null for non-identifier callees (e.g. it.each(t)
// whose callee is itself a CallExpression), which naturally skips dynamic forms.
function calleeName(node) {
  let c = node.callee;
  if (c && c.type === 'MemberExpression') c = c.object;
  if (c && c.type === 'Identifier') return c.name;
  return null;
}

// staticTitle returns the literal title of a block, or null when it isn't a
// plain string (interpolated/computed titles can't be matched to a report Name).
function staticTitle(node) {
  const a = node.arguments && node.arguments[0];
  if (!a) return null;
  if (a.type === 'StringLiteral') return a.value;
  if (a.type === 'TemplateLiteral' && a.expressions.length === 0) {
    return a.quasis.map((q) => q.value.cooked).join('');
  }
  return null;
}

// callbackFn returns the function/arrow passed as a body argument.
function callbackFn(node) {
  for (let i = 1; i < (node.arguments || []).length; i++) {
    const a = node.arguments[i];
    if (a && (a.type === 'FunctionExpression' || a.type === 'ArrowFunctionExpression')) return a;
  }
  return null;
}

// bodySig hashes the normalized AST of a callback body. Source positions and
// comments are excluded, so it is stable across reformatting/comment edits.
function bodySig(fn) {
  const h = crypto.createHash('sha256');
  (function walk(n) {
    if (!n || typeof n.type !== 'string') return;
    h.update(n.type + ';');
    if (n.type === 'Identifier') h.update('id:' + n.name + ';');
    else if (n.type === 'StringLiteral' || n.type === 'NumericLiteral' || n.type === 'BooleanLiteral') {
      h.update('lit:' + String(n.value) + ';');
    }
    for (const k of Object.keys(n)) {
      if (SKIP_KEYS.has(k)) continue;
      const v = n[k];
      if (Array.isArray(v)) v.forEach(walk);
      else if (v && typeof v === 'object' && typeof v.type === 'string') walk(v);
    }
  })(fn.body);
  return h.digest('hex').slice(0, 16);
}

const out = {};
for (const file of process.argv.slice(2)) {
  const fns = {};
  try {
    const code = fs.readFileSync(file, 'utf8');
    const ast = parser.parse(code, {
      sourceType: 'unambiguous',
      errorRecovery: true,
      plugins: ['jsx', 'typescript'],
    });
    const stack = [];
    (function visit(node) {
      if (!node || typeof node.type !== 'string') return;
      if (node.type === 'CallExpression') {
        const name = calleeName(node);
        if (DESCRIBE_FNS.has(name)) {
          const title = staticTitle(node);
          const fn = callbackFn(node);
          if (title !== null && fn) {
            stack.push(title);
            visit(fn.body);
            stack.pop();
            return;
          }
        } else if (TEST_FNS.has(name)) {
          const title = staticTitle(node);
          const fn = callbackFn(node);
          if (title !== null && fn) fns[stack.concat(title).join(' > ')] = bodySig(fn);
          return;
        }
      }
      for (const k of Object.keys(node)) {
        if (SKIP_KEYS.has(k)) continue;
        const v = node[k];
        if (Array.isArray(v)) v.forEach(visit);
        else if (v && typeof v === 'object' && typeof v.type === 'string') visit(v);
      }
    })(ast.program);
  } catch (e) {
    // leave fns empty for this file; a parse failure must not fail discovery
  }
  out[file] = fns;
}
process.stdout.write(JSON.stringify(out));
`

// astSignatures runs the embedded Node helper over the given files (relative to
// workdir) and returns map[path]map[name]signature, keyed to match the per-test
// Name parseReport builds. Best-effort: if node is unavailable or the helper
// fails, it returns nil and execution proceeds with empty signatures.
func (a *Adapter) astSignatures(ctx context.Context, workdir string, paths []string) map[string]map[string]string {
	if len(paths) == 0 {
		return nil
	}
	scriptFile, err := os.CreateTemp("", "teo-jsast-*.cjs")
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
	for _, node := range a.nodeBins() {
		// #nosec G204 -- caller-supplied test paths; the same SPI exemption the
		// rest of this adapter relies on (see docs/adapters/spi.md).
		cmd := exec.CommandContext(ctx, node, args...)
		cmd.Dir = workdir
		out, err := cmd.Output()
		if err != nil {
			continue // try the next node binary name
		}
		var res map[string]map[string]string
		if err := json.Unmarshal(out, &res); err != nil {
			return nil
		}
		return res
	}
	return nil
}

// nodeBins returns the node binary names to try, in order.
func (a *Adapter) nodeBins() []string {
	if a.NodeBin != "" {
		return []string{a.NodeBin}
	}
	return []string{"node"}
}

// distinctPaths returns the unique Path values across entries, preserving order.
func distinctPaths(tests []model.TestEntry) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tests))
	for _, t := range tests {
		if _, ok := seen[t.Path]; ok {
			continue
		}
		seen[t.Path] = struct{}{}
		out = append(out, t.Path)
	}
	return out
}
