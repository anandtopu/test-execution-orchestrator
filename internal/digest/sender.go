package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// Sender delivers a rendered digest to one recipient. Concrete senders are
// SMTP and Slack; multiplex is a fanout that calls every configured sender.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Message is a fully-rendered digest payload, format-agnostic.
type Message struct {
	Owner    string // CODEOWNERS team or @user identifier (used by Slack)
	Email    string // empty if no email destination
	Subject  string
	HTML     string
	Text     string
}

// --- SMTP ------------------------------------------------------------------

// SMTPSender sends mail via standard SMTP with optional STARTTLS auth.
// Configuration mirrors common Helm-chart fields.
type SMTPSender struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
	HTTP     *http.Client // unused; reserved
	dialer   smtpDialer   // injectable for tests
}

// smtpDialer is the minimal abstraction we need for tests.
type smtpDialer interface {
	Send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}

type stdDialer struct{}

func (stdDialer) Send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return smtp.SendMail(addr, auth, from, to, msg)
}

// NewSMTPSender returns a configured SMTPSender using the stdlib dialer.
func NewSMTPSender(host string, port int, from, user, pass string) *SMTPSender {
	return &SMTPSender{
		Host: host, Port: port, From: from, Username: user, Password: pass,
		dialer: stdDialer{},
	}
}

// Send implements Sender.
func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	if m.Email == "" {
		return nil // SMTP delivery requires an email; silently skip
	}
	if s.Host == "" {
		return errors.New("SMTP host not configured")
	}
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)

	body := s.composeMIME(m)
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}

	done := make(chan error, 1)
	go func() {
		done <- s.dialer.Send(addr, auth, s.From, []string{m.Email}, body)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// composeMIME builds a multipart/alternative message with both HTML and text.
func (s *SMTPSender) composeMIME(m Message) []byte {
	boundary := "teo-digest-boundary-" + fmt.Sprintf("%d", time.Now().UnixNano())
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", s.From)
	fmt.Fprintf(&b, "To: %s\r\n", m.Email)
	fmt.Fprintf(&b, "Subject: %s\r\n", m.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	// text/plain part
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(m.Text)
	b.WriteString("\r\n")

	// text/html part
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	b.WriteString(m.HTML)
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

// --- Slack -----------------------------------------------------------------

// SlackSender posts to an incoming-webhook URL with a Markdown payload built
// from Message.Text (HTML is too rich for Slack chat). The webhook URL is
// expected to come from a k8s Secret per ADR-0015.
type SlackSender struct {
	WebhookURL string
	HTTP       *http.Client
}

// NewSlackSender returns a SlackSender with a 10s HTTP timeout.
func NewSlackSender(url string) *SlackSender {
	return &SlackSender{WebhookURL: url, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

type slackPayload struct {
	Text string `json:"text"`
}

// Send implements Sender.
func (s *SlackSender) Send(ctx context.Context, m Message) error {
	if s.WebhookURL == "" {
		return errors.New("Slack webhook not configured")
	}
	payload := slackPayload{Text: buildSlackText(m)}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack webhook: %s", resp.Status)
	}
	return nil
}

func buildSlackText(m Message) string {
	// Slack accepts a subset of Markdown ("mrkdwn"); the plain-text rendering
	// is already close enough — we just bold the heading.
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(m.Subject)
	b.WriteString("*\n")
	b.WriteString("```\n")
	b.WriteString(m.Text)
	b.WriteString("\n```")
	return b.String()
}

// --- Multiplex -------------------------------------------------------------

// Multiplex fans a Message out to multiple Senders. A failure in one delivery
// channel is logged but does not block other channels — the digest is informational.
type Multiplex struct {
	Senders []Sender
	OnError func(name string, err error)
}

// Send implements Sender.
func (m *Multiplex) Send(ctx context.Context, msg Message) error {
	var firstErr error
	for i, s := range m.Senders {
		if err := s.Send(ctx, msg); err != nil {
			if m.OnError != nil {
				m.OnError(fmt.Sprintf("sender#%d", i), err)
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
