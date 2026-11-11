package desk

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestBuildMessageHeaders(t *testing.T) {
	raw, err := buildMessage(outgoing{
		FromName: "Anna Agent (Support)", From: "support@desk.test",
		To: []string{"alice@example.com"}, CC: []string{"bob@example.com", "carol@example.com"},
		Subject: replySubject("Hotel für Gäste", 12, true), MessageID: "<desk.1@desk.test>",
		InReplyTo: "<c2@example.com>", References: []string{"<c1@example.com>", "<desk.0@desk.test>", "<c2@example.com>"},
		Text: "Hello Alice", HTML: "<p>Hello <b>Alice</b></p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if p.Subject != "Re: Hotel für Gäste [#12]" {
		t.Fatalf("subject %q", p.Subject)
	}
	if p.FromEmail != "support@desk.test" || p.FromName != "Anna Agent (Support)" {
		t.Fatalf("from %q %q", p.FromEmail, p.FromName)
	}
	if strings.Join(p.CC, ",") != "bob@example.com,carol@example.com" {
		t.Fatalf("cc %v", p.CC)
	}
	if p.MessageID != "desk.1@desk.test" {
		t.Fatalf("message id %q", p.MessageID)
	}
	// In-Reply-To first, then References in order.
	want := []string{"c2@example.com", "c1@example.com", "desk.0@desk.test", "c2@example.com"}
	if strings.Join(p.References, " ") != strings.Join(want, " ") {
		t.Fatalf("references %v", p.References)
	}
	if p.Text != "Hello Alice" || !strings.Contains(p.HTML, "<b>Alice</b>") {
		t.Fatalf("bodies %q %q", p.Text, p.HTML)
	}
	if strings.Contains(string(raw), "Auto-Submitted") {
		t.Fatal("a reply is not automatic")
	}
}

func TestBuildMessageWithAttachmentsAndExtraHeaders(t *testing.T) {
	raw, err := buildMessage(outgoing{
		FromName: "Support", From: "support@desk.test", To: []string{"x@example.com"}, Subject: "Fwd: Badge",
		MessageID: "<desk.2@desk.test>", Text: "see attached", HTML: "<p>see attached</p>",
		Attachments: []outAttachment{{Filename: "photo.png", ContentType: "image/png", Data: []byte(strings.Repeat("\x89PNG", 50))}},
		Extra:       map[string]string{"Auto-Submitted": "auto-replied"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Auto-Submitted: auto-replied\r\n") {
		t.Fatal("extra header missing")
	}
	p := parse(t, raw)
	if len(p.Attachments) != 1 || p.Attachments[0].Filename != "photo.png" || len(p.Attachments[0].Data) != 200 {
		t.Fatalf("attachments %+v", p.Attachments)
	}
	if p.Text != "see attached" {
		t.Fatalf("text %q", p.Text)
	}
}

func TestReplySubject(t *testing.T) {
	cases := []struct {
		subject string
		answer  bool
		want    string
	}{
		{"Hotel", false, "Hotel [#3]"},
		{"Hotel", true, "Re: Hotel [#3]"},
		{"RE: Hotel", true, "RE: Hotel [#3]"},
		{"Re: Hotel [#3]", true, "Re: Hotel [#3]"},
	}
	for _, c := range cases {
		if got := replySubject(c.subject, 3, c.answer); got != c.want {
			t.Errorf("replySubject(%q, %v) = %q, want %q", c.subject, c.answer, got, c.want)
		}
	}
}

func TestAfterFailure(t *testing.T) {
	for attempts, want := range map[int]time.Duration{1: time.Minute, 2: 5 * time.Minute, 3: 15 * time.Minute, 4: time.Hour} {
		failed, wait := afterFailure(attempts)
		if failed || wait != want {
			t.Errorf("attempt %d: failed=%v wait=%s, want %s", attempts, failed, wait, want)
		}
	}
	if failed, _ := afterFailure(len(retryDelays) + 1); !failed {
		t.Fatal("after the last delay the reply must fail")
	}
}

func TestComposeBodies(t *testing.T) {
	h, text := composeBodies("", `<h2>Welcome</h2><p>Line one<br>Line two</p><ul><li>a</li><li>b</li></ul><script>x()</script><a href="https://example.com">link</a>`)
	if strings.Contains(h, "script") {
		t.Fatalf("html not sanitised: %s", h)
	}
	for _, want := range []string{"Welcome", "Line one\nLine two", "- a", "- b", "link"} {
		if !strings.Contains(text, want) {
			t.Errorf("plain text %q lacks %q", text, want)
		}
	}
	h, text = composeBodies("plain <only>", "")
	if text != "plain <only>" || !strings.Contains(h, "&lt;only&gt;") {
		t.Fatalf("%q %q", h, text)
	}
	if _, text := composeBodies("  ", "<p> </p>"); text != "" {
		t.Fatalf("empty message: %q", text)
	}
}

// fakeSMTP accepts one message and then drops the connection on QUIT.
func fakeSMTP(t *testing.T) (port int, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		w("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					got <- data.String()
					w("250 queued")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				w("250 fake")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				w("250 ok")
			case cmd == "DATA":
				inData = true
				w("354 go ahead")
			case cmd == "QUIT":
				return // no 221: Quit() fails
			default:
				w("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestQuitFailureAfterDataCountsAsSent(t *testing.T) {
	port, got := fakeSMTP(t)
	s := &Service{dev: true}
	cfg := SMTPSettings{Host: "127.0.0.1", Port: port, Security: "none", moduleWide: true}
	err := s.sendSMTP(context.Background(), cfg, "support@desk.test", []string{"alice@example.com"}, []byte("Subject: x\r\n\r\nbody\r\n"), zerolog.Nop())
	if err != nil {
		t.Fatalf("accepted message must count as sent: %v", err)
	}
	select {
	case msg := <-got:
		if !strings.Contains(msg, "body") {
			t.Fatalf("message %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never got the message")
	}
}
