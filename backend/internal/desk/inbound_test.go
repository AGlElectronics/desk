package desk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parse(t *testing.T, raw []byte) *ParsedMessage {
	t.Helper()
	p, err := ParseMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseOutlookInlineImage(t *testing.T) {
	p := parse(t, fixture(t, "outlook_inline_image.eml"))
	if p.FromEmail != "maria.example@example.com" || p.FromName != "Maria Example" {
		t.Fatalf("from %q %q", p.FromEmail, p.FromName)
	}
	if p.MessageID != "AM0PR01MB1234ABCD@AM0PR01MB1234.eurprd01.prod.exchangelabs.com" {
		t.Fatalf("message id %q", p.MessageID)
	}
	if len(p.CC) != 1 || p.CC[0] != "lead@example.com" {
		t.Fatalf("cc %v", p.CC)
	}
	if len(p.Attachments) != 1 {
		t.Fatalf("attachments %+v", p.Attachments)
	}
	a := p.Attachments[0]
	if a.ContentID != "image001.png@01DB1234.ABCDEF00" || a.ContentType != "image/png" || len(a.Data) == 0 {
		t.Fatalf("inline image %+v", a)
	}
	if p.Automatic() {
		t.Fatal("a person's mail is not automatic")
	}

	id := uuid.New()
	htmlBody, text := p.bodies(map[string]uuid.UUID{strings.ToLower(a.ContentID): id})
	if !strings.Contains(htmlBody, `src="/api/v1/attachments/`+id.String()+`"`) {
		t.Fatalf("cid not rewritten: %s", htmlBody)
	}
	for _, bad := range []string{"<script", "alert(1)", "onclick", "steal()"} {
		if strings.Contains(htmlBody, bad) {
			t.Fatalf("unsafe HTML kept (%s): %s", bad, htmlBody)
		}
	}
	if !strings.Contains(htmlBody, `target="_blank"`) || !strings.Contains(htmlBody, "noopener") || !strings.Contains(htmlBody, "noreferrer") {
		t.Fatalf("links must open with noopener noreferrer: %s", htmlBody)
	}
	if !strings.Contains(text, "badge photo") {
		t.Fatalf("text %q", text)
	}
}

func TestUnknownCIDIsLeftAlone(t *testing.T) {
	p := &ParsedMessage{HTML: `<img src="cid:missing@x">`}
	htmlBody, _ := p.bodies(nil)
	if strings.Contains(htmlBody, "/api/v1/attachments/") {
		t.Fatalf("unexpected rewrite: %s", htmlBody)
	}
}

func TestParseOutOfOffice(t *testing.T) {
	p := parse(t, fixture(t, "out_of_office.eml"))
	if !p.AutoReply || !p.Automatic() || p.Bounce {
		t.Fatalf("auto-reply not detected: %+v", p)
	}
	if len(p.References) != 1 || p.References[0] != "desk.reply-1@desk.test" {
		t.Fatalf("references %v", p.References)
	}
}

func TestParseBounce(t *testing.T) {
	p := parse(t, fixture(t, "bounce.eml"))
	if !p.Bounce || !p.Automatic() {
		t.Fatalf("bounce not detected: %+v", p)
	}
	if !strings.Contains(p.Text, "User unknown") {
		t.Fatalf("text %q", p.Text)
	}
}

func TestParseLatin1(t *testing.T) {
	p := parse(t, fixture(t, "latin1_qp.eml"))
	if p.Subject != "Frage zur Anmeldung über die Webseite" {
		t.Fatalf("subject %q", p.Subject)
	}
	if p.FromName != "Jürgen Müller" {
		t.Fatalf("name %q", p.FromName)
	}
	if !strings.Contains(p.Text, "Grüße aus München") {
		t.Fatalf("text %q", p.Text)
	}
}

func TestParseCleansNULAndInvalidUTF8(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: s@desk.test\r\nSubject: bad\xff\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello\x00world \xc3\x28\r\n")
	p := parse(t, raw)
	if strings.ContainsRune(p.Text, 0) || !strings.Contains(p.Text, "helloworld") {
		t.Fatalf("text %q", p.Text)
	}
	if !strings.Contains(p.Text, "\uFFFD") {
		t.Fatalf("invalid UTF-8 not replaced: %q", p.Text)
	}
}

func TestParseCapsParts(t *testing.T) {
	var b strings.Builder
	b.WriteString("From: a@example.com\r\nTo: s@desk.test\r\nSubject: many\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=XX\r\n\r\n")
	for i := 0; i < maxParts+20; i++ {
		fmt.Fprintf(&b, "--XX\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"f%d.bin\"\r\n\r\nabc\r\n", i)
	}
	b.WriteString("--XX--\r\n")
	p := parse(t, []byte(b.String()))
	if !p.Truncated {
		t.Fatal("expected Truncated")
	}
	if len(p.Attachments) != maxAttachments {
		t.Fatalf("kept %d attachments, want %d", len(p.Attachments), maxAttachments)
	}
	if len(p.Skipped) == 0 {
		t.Fatal("skipped attachments must be listed")
	}
	_, text := p.bodies(nil)
	if !strings.Contains(text, "more than 100 parts") || !strings.Contains(text, "attachments not kept") {
		t.Fatalf("notes missing: %q", text)
	}
}

func TestAttachmentTotalCap(t *testing.T) {
	p := &ParsedMessage{}
	total := 0
	big := make([]byte, 15<<20)
	for i := 0; i < 3; i++ {
		p.addAttachment(&total, ParsedAttachment{Filename: fmt.Sprintf("big%d", i), Data: big})
	}
	if len(p.Attachments) != 2 || len(p.Skipped) != 1 || !strings.Contains(p.Skipped[0], "40 MB in total") {
		t.Fatalf("kept %d, skipped %v", len(p.Attachments), p.Skipped)
	}
}
