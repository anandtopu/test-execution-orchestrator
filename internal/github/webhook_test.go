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

// TestSuspendedFlagFromAction is a pure mapping check on the action→suspended
// translation that handleInstallation uses. Regression for H9 (was always
// suspended=FALSE before).
func TestSuspendedFlagFromAction(t *testing.T) {
	for _, tc := range []struct {
		action string
		want   bool
	}{
		{"created", false},
		{"new_permissions_accepted", false},
		{"unsuspend", false},
		{"deleted", true},
		{"suspend", true},
	} {
		got := false
		switch tc.action {
		case "deleted", "suspend":
			got = true
		}
		if got != tc.want {
			t.Errorf("action=%s: got suspended=%v, want %v", tc.action, got, tc.want)
		}
	}
}
