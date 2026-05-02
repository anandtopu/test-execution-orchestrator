package digest

import "testing"

func TestOwnerMatchesUserHandle(t *testing.T) {
	if !ownerMatchesUser("@alice", "alice@example.com", []string{"alice"}) {
		t.Error("should match local-part")
	}
	if ownerMatchesUser("@bob", "alice@example.com", []string{"alice"}) {
		t.Error("non-matching handle must not match")
	}
}

func TestOwnerTeamHandleNeverMatchesUser(t *testing.T) {
	// Team handles ("@org/team") cannot resolve to a single user in dry-run.
	if ownerMatchesUser("@org/team-foo", "alice@example.com", []string{"alice"}) {
		t.Error("team handle must not match a user")
	}
}

func TestOwnerEmptyDoesNotMatch(t *testing.T) {
	if ownerMatchesUser("", "alice@x", []string{"alice"}) {
		t.Error("empty owner must not match")
	}
}
