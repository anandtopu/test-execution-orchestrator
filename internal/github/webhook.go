// Package github implements the GitHub App webhook receiver and Check Runs API
// integration (FR-901..904, ADR-0014).
package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Webhook handles incoming events from GitHub.
type Webhook struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	Secret []byte // HMAC shared secret
}

// VerifySignature returns nil if X-Hub-Signature-256 matches the body.
func VerifySignature(body, secret []byte, sigHeader string) error {
	const prefix = "sha256="
	if !strings.HasPrefix(sigHeader, prefix) {
		return errors.New("missing or malformed signature")
	}
	want, err := hex.DecodeString(sigHeader[len(prefix):])
	if err != nil {
		return errors.New("bad hex in signature")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	got := mac.Sum(nil)
	if !hmac.Equal(want, got) {
		return errors.New("signature mismatch")
	}
	return nil
}

// ServeHTTP implements http.Handler.
func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, 5<<20))
	if err != nil {
		http.Error(rw, "body too large or unreadable", http.StatusBadRequest)
		return
	}
	if err := VerifySignature(body, w.Secret, r.Header.Get("X-Hub-Signature-256")); err != nil {
		w.Logger.Warn("webhook signature failed", "err", err)
		http.Error(rw, "unauthorized", http.StatusUnauthorized)
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	w.Logger.Info("github webhook", "event", event, "delivery", deliveryID)

	switch event {
	case "installation", "installation_repositories":
		w.handleInstallation(r.Context(), body)
	case "push":
		// Delegated to the Check Run creator in a real impl.
	case "ping":
		// no-op; GitHub sends this to verify connectivity.
	}
	rw.WriteHeader(http.StatusOK)
}

type installationEvent struct {
	Action       string `json:"action"`
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	} `json:"installation"`
	Repositories []struct {
		FullName string `json:"full_name"`
	} `json:"repositories"`
}

func (w *Webhook) handleInstallation(ctx context.Context, body []byte) {
	var e installationEvent
	if err := json.Unmarshal(body, &e); err != nil {
		w.Logger.Error("decode installation event", "err", err)
		return
	}
	// installation event actions: created, deleted, suspend, unsuspend,
	// new_permissions_accepted. Map them to the suspended flag so a removed
	// or suspended App stops appearing as installed (was previously hard-coded
	// to suspended=FALSE on every action — see audit finding H9).
	suspended := false
	switch e.Action {
	case "deleted", "suspend":
		suspended = true
	}
	_, err := w.Pool.Exec(ctx, `
        INSERT INTO teo.github_installations (id, account_login, account_type, suspended)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (id) DO UPDATE SET
            account_login = EXCLUDED.account_login,
            account_type  = EXCLUDED.account_type,
            suspended     = EXCLUDED.suspended
    `, e.Installation.ID, e.Installation.Account.Login, e.Installation.Account.Type, suspended)
	if err != nil {
		w.Logger.Error("upsert installation", "err", err)
		return
	}
	// Repo enablement only flips on installs, not on suspends/deletes —
	// repo rows survive App removal so historical run data stays joinable.
	if e.Action == "deleted" || e.Action == "suspend" {
		return
	}
	for _, repo := range e.Repositories {
		_, err := w.Pool.Exec(ctx, `
            INSERT INTO teo.repos (vcs, full_name) VALUES ('github', $1)
            ON CONFLICT (vcs, full_name) DO UPDATE SET enabled = TRUE
        `, repo.FullName)
		if err != nil {
			w.Logger.Error("upsert repo", "repo", repo.FullName, "err", err)
		}
	}
}
