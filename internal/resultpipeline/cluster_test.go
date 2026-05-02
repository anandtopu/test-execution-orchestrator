package resultpipeline

import (
	"strings"
	"testing"
)

func TestPythonFingerprintStableAcrossLineNumbers(t *testing.T) {
	a := `Traceback (most recent call last):
  File "/app/svc.py", line 42, in handle_request
    self.do_work()
  File "/app/svc.py", line 91, in do_work
    raise AssertionError("boom")
AssertionError: boom`
	b := `Traceback (most recent call last):
  File "/app/svc.py", line 99, in handle_request
    self.do_work()
  File "/app/svc.py", line 200, in do_work
    raise AssertionError("boom")
AssertionError: boom`
	fpA, _ := FingerprintStack(a)
	fpB, _ := FingerprintStack(b)
	if fpA != fpB {
		t.Fatalf("fingerprints diverged after line-number edits:\nA=%s\nB=%s", fpA, fpB)
	}
}

func TestPythonFingerprintDiffersForDifferentExceptions(t *testing.T) {
	a := `Traceback (most recent call last):
  File "/a.py", line 1, in f
AssertionError: boom`
	b := `Traceback (most recent call last):
  File "/a.py", line 1, in f
ValueError: nope`
	fpA, _ := FingerprintStack(a)
	fpB, _ := FingerprintStack(b)
	if fpA == fpB {
		t.Fatal("different exceptions should produce different fingerprints")
	}
}

func TestGenericFallback(t *testing.T) {
	in := `panic: runtime error: index out of range [3] at 0x4f8a91
goroutine 5
github.com/foo/bar.handle()
	/src/bar.go:99
something with timestamp 1714512000`
	fp, normalized := FingerprintStack(in)
	if fp == "" {
		t.Fatal("empty fingerprint")
	}
	if strings.Contains(normalized, "0x4f8a91") {
		t.Fatalf("hex not stripped: %s", normalized)
	}
}

func TestEmptyInputReturnsEmpty(t *testing.T) {
	fp, _ := FingerprintStack("")
	if fp != "" {
		t.Fatal("empty input should yield empty fingerprint")
	}
}
