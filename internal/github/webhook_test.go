package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifySignatureValid(t *testing.T) {
	secret := []byte("hush")
	body := []byte(`{"hello":"world"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if err := VerifySignature(body, secret, sig); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySignatureInvalid(t *testing.T) {
	if err := VerifySignature([]byte("a"), []byte("k"), "sha256=00"); err == nil {
		t.Fatal("expected error")
	}
	if err := VerifySignature([]byte("a"), []byte("k"), "wrong-format"); err == nil {
		t.Fatal("expected error")
	}
}
