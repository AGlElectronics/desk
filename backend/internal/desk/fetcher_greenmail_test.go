package desk

import (
	"context"
	"fmt"
	"net/smtp"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/testdb"
)

// GreenMail from the dev stack: DESK_TEST_GREENMAIL=localhost (SMTP 3025,
// IMAP 3143). GreenMail creates a mailbox for every address it receives
// mail for, with the address as login and password.
func greenmail(t *testing.T) string {
	t.Helper()
	host := os.Getenv("DESK_TEST_GREENMAIL")
	if host == "" {
		t.Skip("DESK_TEST_GREENMAIL is not set; skipping IMAP test")
	}
	return host
}

func deliver(t *testing.T, host, to, subject, body string, extra ...string) {
	t.Helper()
	msg := "From: Alice Customer <alice@example.com>\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nMessage-ID: <" + uuid.NewString() + "@example.com>\r\n" + strings.Join(extra, "") +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n"
	if err := smtp.SendMail(host+":3025", nil, "alice@example.com", []string{to}, []byte(msg)); err != nil {
		t.Fatalf("deliver: %v", err)
	}
}

func greenmailMailbox(t *testing.T, svc *Service, host string) Mailbox {
	t.Helper()
	addr := "gm-" + uuid.NewString()[:8] + "@desk.test"
	mb, err := svc.CreateMailbox(context.Background(), uuid.MustParse(testdb.DemoOrg), MailboxInput{
		Name: ptr("GreenMail"), Email: ptr(addr),
		IMAPHost: ptr(host), IMAPPort: ptr(3143), IMAPSecurity: ptr("none"),
		IMAPUsername: ptr(addr), IMAPPassword: ptr(addr),
	})
	if err != nil {
		t.Fatal(err)
	}
	return mb
}

func TestFetcherAgainstGreenMail(t *testing.T) {
	host := greenmail(t)
	pool := testdb.New(t)
	ctx := context.Background()
	svc := NewService(pool, Options{Keyring: keyring(t, "k"), Development: true, AllowedMailHosts: []string{host, "127.0.0.1/32", "::1/128"}})
	mb := greenmailMailbox(t, svc, host)
	for i := 1; i <= 3; i++ {
		deliver(t, host, mb.Email, fmt.Sprintf("Question %d", i), "Hello")
	}

	log := zerolog.Nop()
	n, err := svc.fetchMailbox(ctx, mb, log)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("imported %d, want 3", n)
	}
	mb, _ = svc.GetMailbox(ctx, mb.ID)
	if mb.lastUID == 0 || mb.uidValidity == 0 {
		t.Fatalf("UID state not stored: %d/%d", mb.lastUID, mb.uidValidity)
	}

	// Nothing new: nothing imported, last_uid unchanged.
	before := mb.lastUID
	if n, err := svc.fetchMailbox(ctx, mb, log); err != nil || n != 0 {
		t.Fatalf("second run: %d %v", n, err)
	}
	deliver(t, host, mb.Email, "Question 4", "Again")
	mb, _ = svc.GetMailbox(ctx, mb.ID)
	if n, err := svc.fetchMailbox(ctx, mb, log); err != nil || n != 1 {
		t.Fatalf("third run: %d %v", n, err)
	}
	mb, _ = svc.GetMailbox(ctx, mb.ID)
	if mb.lastUID <= before {
		t.Fatalf("last_uid did not move: %d", mb.lastUID)
	}

	// A changed UIDVALIDITY rereads everything; the Message-ID index keeps
	// it from importing duplicates.
	if _, err := pool.Exec(ctx, `UPDATE mailboxes SET uid_validity = 1 WHERE id = $1`, mb.ID); err != nil {
		t.Fatal(err)
	}
	mb, _ = svc.GetMailbox(ctx, mb.ID)
	if n, err := svc.fetchMailbox(ctx, mb, log); err != nil || n != 0 {
		t.Fatalf("after UIDVALIDITY change: %d %v", n, err)
	}
	var convs int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM conversations WHERE mailbox_id = $1`, mb.ID).Scan(&convs)
	if convs != 4 {
		t.Fatalf("conversations %d, want 4", convs)
	}
}

func TestFetcherSkipsUnstorableMessage(t *testing.T) {
	host := greenmail(t)
	pool := testdb.New(t)
	ctx := context.Background()
	svc := NewService(pool, Options{Keyring: keyring(t, "k"), Development: true, AllowedMailHosts: []string{host, "127.0.0.1/32", "::1/128"}})
	mb := greenmailMailbox(t, svc, host)
	deliver(t, host, mb.Email, "No sender", "x")
	deliver(t, host, mb.Email, "Fine", "Hello")

	// Make every insert into threads fail for the first message only.
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION reject_first() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.body_text LIKE 'x%' THEN RAISE EXCEPTION 'poison'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_first BEFORE INSERT ON threads FOR EACH ROW EXECUTE FUNCTION reject_first();`); err != nil {
		t.Fatal(err)
	}
	n, err := svc.fetchMailbox(ctx, mb, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("imported %d, want 1 (the poison message is skipped)", n)
	}
	fails, err := svc.ImportFailures(ctx, mb.ID)
	if err != nil || len(fails) != 1 || fails[0].Subject != "No sender" {
		t.Fatalf("import failures: %+v %v", fails, err)
	}
}

func TestFetchFailureBacksOff(t *testing.T) {
	host := greenmail(t)
	pool := testdb.New(t)
	ctx := context.Background()
	svc := NewService(pool, Options{Keyring: keyring(t, "k"), Development: true, AllowedMailHosts: []string{host, "127.0.0.1/32", "::1/128"}})
	mb := greenmailMailbox(t, svc, host)
	deliver(t, host, mb.Email, "hi", "x")
	if _, err := svc.UpdateMailbox(ctx, mb.ID, MailboxInput{IMAPPassword: ptr("wrong")}); err != nil {
		t.Fatal(err)
	}
	mb, _ = svc.GetMailbox(ctx, mb.ID)
	svc.fetchOne(ctx, mb, zerolog.Nop())
	mb, _ = svc.GetMailbox(ctx, mb.ID)
	if mb.FetchFailures != 1 || mb.NextFetchAt == nil || !mb.NextFetchAt.After(time.Now()) {
		t.Fatalf("no back-off: %+v", mb)
	}
	if mb.LastFetchError != errLogin.Error() {
		t.Fatalf("error shown: %q", mb.LastFetchError)
	}
}
