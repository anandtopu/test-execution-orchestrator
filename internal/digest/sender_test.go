package digest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- SMTP ------------------------------------------------------------------

type captureDialer struct {
	mu   sync.Mutex
	addr string
	from string
	to   []string
	body []byte
	err  error
}

func (c *captureDialer) Send(addr string, _ smtp.Auth, from string, to []string, msg []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addr = addr
	c.from = from
	c.to = append([]string(nil), to...)
	c.body = append([]byte(nil), msg...)
	return c.err
}

func TestSMTPSendRoundtrip(t *testing.T) {
	d := &captureDialer{}
	s := &SMTPSender{
		Host: "smtp.example.com", Port: 587, From: "noreply@teo.dev",
		dialer: d,
	}
	err := s.Send(context.Background(), Message{
		Email: "alice@example.com", Subject: "TEO digest", HTML: "<b>hi</b>", Text: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.addr != "smtp.example.com:587" {
		t.Errorf("addr = %s", d.addr)
	}
	if d.from != "noreply@teo.dev" {
		t.Errorf("from = %s", d.from)
	}
	if len(d.to) != 1 || d.to[0] != "alice@example.com" {
		t.Errorf("to = %v", d.to)
	}
	body := string(d.body)
	for _, want := range []string{"To: alice@example.com", "Subject: TEO digest", "<b>hi</b>", "multipart/alternative"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestSMTPSkipsWhenNoEmail(t *testing.T) {
	d := &captureDialer{}
	s := &SMTPSender{Host: "h", Port: 25, dialer: d}
	if err := s.Send(context.Background(), Message{Email: ""}); err != nil {
		t.Fatal(err)
	}
	if d.addr != "" {
		t.Error("dialer should not be called when Email is empty")
	}
}

func TestSMTPRespectsContextCancel(t *testing.T) {
	// Dialer that hangs forever
	hang := &captureDialer{}
	hang.err = nil
	s := &SMTPSender{Host: "h", Port: 25, dialer: hangingDialer{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.Send(ctx, Message{Email: "a@b"})
	if err == nil {
		t.Error("expected ctx error")
	}
}

type hangingDialer struct{}

func (hangingDialer) Send(_ string, _ smtp.Auth, _ string, _ []string, _ []byte) error {
	select {} // block forever
}

func TestSMTPHostRequired(t *testing.T) {
	s := &SMTPSender{dialer: &captureDialer{}}
	err := s.Send(context.Background(), Message{Email: "a@b"})
	if err == nil {
		t.Error("expected error when host empty")
	}
}

// --- Slack -----------------------------------------------------------------

func TestSlackSenderPostsExpectedBody(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		got = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := NewSlackSender(srv.URL)
	err := s.Send(context.Background(), Message{
		Subject: "TEO digest — alice", Text: "you own 3 tests",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "TEO digest") {
		t.Errorf("body missing subject: %s", got)
	}
	if !strings.Contains(got, "you own 3 tests") {
		t.Errorf("body missing text: %s", got)
	}
}

func TestSlackSenderFailsOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	s := NewSlackSender(srv.URL)
	err := s.Send(context.Background(), Message{Subject: "x", Text: "y"})
	if err == nil {
		t.Error("expected error on 400 response")
	}
}

func TestSlackSenderRequiresWebhook(t *testing.T) {
	s := NewSlackSender("")
	err := s.Send(context.Background(), Message{})
	if err == nil {
		t.Error("expected error when webhook is empty")
	}
}

// --- Multiplex -------------------------------------------------------------

type stubSender struct {
	called bool
	err    error
}

func (s *stubSender) Send(_ context.Context, _ Message) error {
	s.called = true
	return s.err
}

func TestMultiplexCallsAllEvenOnFailure(t *testing.T) {
	a := &stubSender{err: errors.New("a-fail")}
	b := &stubSender{}
	var captured []string
	m := &Multiplex{
		Senders: []Sender{a, b},
		OnError: func(name string, _ error) { captured = append(captured, name) },
	}
	err := m.Send(context.Background(), Message{})
	if err == nil {
		t.Error("expected first error to propagate")
	}
	if !a.called || !b.called {
		t.Error("both senders should be called even when one fails")
	}
	if len(captured) != 1 {
		t.Errorf("OnError called %d times, want 1", len(captured))
	}
}

// Compile-time interface check
var _ Sender = (*SMTPSender)(nil)
var _ Sender = (*SlackSender)(nil)
var _ Sender = (*Multiplex)(nil)
var _ time.Duration // keep "time" import meaningful even if other tests change
