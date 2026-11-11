package desk

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AGlElectronics/desk/backend/internal/secretbox"
	"github.com/AGlElectronics/desk/backend/internal/testdb"
)

func keyring(t *testing.T, current string, previous ...string) *secretbox.Keyring {
	t.Helper()
	k, err := secretbox.New(current, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestPasswordsAreBoundToMailboxAndColumn(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	svc := NewService(pool, Options{Keyring: keyring(t, "key-one"), Development: true})
	org := uuid.MustParse(testdb.DemoOrg)
	pw := "imap-secret"
	a, err := svc.CreateMailbox(ctx, org, MailboxInput{Name: ptr("A"), Email: ptr("a@desk.test"), IMAPPassword: &pw})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateMailbox(ctx, org, MailboxInput{Name: ptr("B"), Email: ptr("b@desk.test")})
	if err != nil {
		t.Fatal(err)
	}
	a, _ = svc.GetMailbox(ctx, a.ID)
	if got, err := svc.decrypt(a.ID, "imap_password", a.imapPasswordEnc); err != nil || got != pw {
		t.Fatalf("own column: %q %v", got, err)
	}
	if !strings.HasPrefix(a.imapPasswordEnc, "v1.") {
		t.Fatalf("stored format %q", a.imapPasswordEnc)
	}
	// Copy A's ciphertext into B's row and into A's SMTP column.
	if _, err := pool.Exec(ctx, `UPDATE mailboxes SET imap_password_enc = $2, smtp_password_enc = $2 WHERE id = $1`, b.ID, a.imapPasswordEnc); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.decrypt(b.ID, "imap_password", a.imapPasswordEnc); err == nil {
		t.Fatal("ciphertext moved to another mailbox must not open")
	}
	if _, err := svc.decrypt(a.ID, "smtp_password", a.imapPasswordEnc); err == nil {
		t.Fatal("ciphertext moved to another column must not open")
	}
}

func TestReencryptAfterRotation(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	org := uuid.MustParse(testdb.DemoOrg)
	old := NewService(pool, Options{Keyring: keyring(t, "old-key"), Development: true})
	imapPW, smtpPW := "imap-pw", "smtp-pw"
	mb, err := old.CreateMailbox(ctx, org, MailboxInput{
		Name: ptr("Support"), Email: ptr("support@desk.test"), IMAPPassword: &imapPW, SMTPPassword: &smtpPW,
	})
	if err != nil {
		t.Fatal(err)
	}

	rotated := NewService(pool, Options{Keyring: keyring(t, "new-key", "old-key"), Development: true})
	res, err := rotated.Reencrypt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Resealed != 2 || len(res.Failed) != 0 {
		t.Fatalf("got %+v", res)
	}
	again, _ := rotated.Reencrypt(ctx)
	if again.Resealed != 0 || again.Current != 2 {
		t.Fatalf("second run should find everything current: %+v", again)
	}

	newOnly := NewService(pool, Options{Keyring: keyring(t, "new-key"), Development: true})
	stored, _ := newOnly.GetMailbox(ctx, mb.ID)
	if got, err := newOnly.decrypt(mb.ID, "imap_password", stored.imapPasswordEnc); err != nil || got != imapPW {
		t.Fatalf("imap after rotation: %q %v", got, err)
	}
	if got, err := newOnly.decrypt(mb.ID, "smtp_password", stored.smtpPasswordEnc); err != nil || got != smtpPW {
		t.Fatalf("smtp after rotation: %q %v", got, err)
	}

	lost := NewService(pool, Options{Keyring: keyring(t, "unrelated-key"), Development: true})
	res, err = lost.Reencrypt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 2 {
		t.Fatalf("a lost key must report every value: %+v", res)
	}
}

func ptr[T any](v T) *T { return &v }
