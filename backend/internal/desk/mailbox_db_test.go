package desk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
	"github.com/AGlElectronics/desk/backend/internal/testdb"
)

func validationField(t *testing.T, err error, field string) {
	t.Helper()
	var he *httperror.Error
	if !errors.As(err, &he) {
		t.Fatalf("expected a validation error on %s, got %v", field, err)
	}
	details, _ := he.Details.(map[string]string)
	if _, ok := details[field]; !ok {
		t.Fatalf("expected an issue on %s, got %+v", field, he.Details)
	}
}

func TestHostChangeNeedsPasswordAgain(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	svc := NewService(pool, Options{Keyring: keyring(t, "k"), Development: true})
	org := uuid.MustParse(testdb.DemoOrg)
	mb, err := svc.CreateMailbox(ctx, org, MailboxInput{
		Name: ptr("Support"), Email: ptr("support@desk.test"),
		IMAPHost: ptr("imap.example.com"), IMAPUsername: ptr("support"), IMAPPassword: ptr("pw"),
		SMTPHost: ptr("smtp.example.com"), SMTPUsername: ptr("support"), SMTPPassword: ptr("pw"),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.UpdateMailbox(ctx, mb.ID, MailboxInput{IMAPHost: ptr("evil.example.net")})
	validationField(t, err, "imap_password")
	_, err = svc.UpdateMailbox(ctx, mb.ID, MailboxInput{IMAPUsername: ptr("someone-else")})
	validationField(t, err, "imap_password")
	_, err = svc.UpdateMailbox(ctx, mb.ID, MailboxInput{SMTPHost: ptr("evil.example.net")})
	validationField(t, err, "smtp_password")

	// Same host (different case) or other fields: no password needed.
	if _, err := svc.UpdateMailbox(ctx, mb.ID, MailboxInput{IMAPHost: ptr("IMAP.example.com"), Signature: ptr("-- Support")}); err != nil {
		t.Fatal(err)
	}
	// With the password in the same request the move is fine and resets the UID state.
	if _, err := pool.Exec(ctx, `UPDATE mailboxes SET last_uid = 42, uid_validity = 7, fetch_failures = 3, next_fetch_at = now() + interval '1 hour' WHERE id = $1`, mb.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMailbox(ctx, mb.ID, MailboxInput{IMAPHost: ptr("imap2.example.com"), IMAPPassword: ptr("new")}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetMailbox(ctx, mb.ID)
	if got.lastUID != 0 || got.uidValidity != 0 || got.FetchFailures != 0 || got.NextFetchAt != nil {
		t.Fatalf("state not reset: %+v", got)
	}
	if pw, _ := svc.decrypt(mb.ID, "imap_password", got.imapPasswordEnc); pw != "new" {
		t.Fatalf("password %q", pw)
	}
}

func TestPlaintextOnlyInDevelopment(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	prod := NewService(pool, Options{Keyring: keyring(t, "k")})
	org := uuid.MustParse(testdb.DemoOrg)
	_, err := prod.CreateMailbox(ctx, org, MailboxInput{Name: ptr("A"), Email: ptr("a@desk.test"), IMAPSecurity: ptr("none")})
	validationField(t, err, "imap_security")
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM mailboxes`).Scan(&n)
	if n != 0 {
		t.Fatal("a rejected create must not leave a mailbox behind")
	}
	_, err = prod.CreateMailbox(ctx, org, MailboxInput{Name: ptr("A"), Email: ptr("a@desk.test"), SMTPSecurity: ptr("none")})
	validationField(t, err, "smtp_security")

	dev := NewService(pool, Options{Keyring: keyring(t, "k"), Development: true})
	if _, err := dev.CreateMailbox(ctx, org, MailboxInput{Name: ptr("A"), Email: ptr("a@desk.test"), IMAPSecurity: ptr("none")}); err != nil {
		t.Fatal(err)
	}
}

func TestBackoff(t *testing.T) {
	cases := map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 3: 4 * time.Minute, 7: time.Hour, 50: time.Hour}
	for failures, want := range cases {
		if got := backoff(time.Minute, failures); got != want {
			t.Errorf("backoff(1m, %d) = %s, want %s", failures, got, want)
		}
	}
}
