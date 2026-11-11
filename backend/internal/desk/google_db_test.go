package desk

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/blob"
	"github.com/AGlElectronics/desk/backend/internal/mailprovider"
	"github.com/AGlElectronics/desk/backend/internal/testdb"
)

const shared = "desk@example.org"

type googleEnv struct {
	svc   *Service
	fake  *mailprovider.Fake
	blobs *blob.Memory
	org   uuid.UUID
	anna  *Person
}

func newGoogleEnv(t *testing.T) googleEnv {
	t.Helper()
	pool := testdb.New(t)
	fake := mailprovider.NewFake()
	fake.AddUser(shared)
	blobs := blob.NewMemory()
	svc := NewService(pool, Options{
		Keyring: keyring(t, "k"), Development: true, Blobs: blobs, NotifyFrom: "Desk <" + shared + ">",
		BaseURL: "https://desk.example.org",
		Google:  &GoogleOptions{Provider: fake, Domain: "example.org", SharedMailbox: shared, Label: "Desk"},
	})
	return googleEnv{svc: svc, fake: fake, blobs: blobs, org: uuid.MustParse(testdb.DemoOrg), anna: agent(t, pool, "Anna")}
}

func (e googleEnv) mailbox(t *testing.T, connector, email string) Mailbox {
	t.Helper()
	mb, err := e.svc.CreateMailbox(context.Background(), e.org, MailboxInput{Connector: ptr(connector), Name: ptr(strings.Split(email, "@")[0]), Email: ptr(email)})
	if err != nil {
		t.Fatal(err)
	}
	return mb
}

func customerMail(to, subject, msgID string) []byte {
	return []byte("From: Alice <alice@example.com>\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nMessage-ID: <" + msgID + ">\r\n\r\nHello\r\n")
}

func TestGoogleUserMailbox(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	mb := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	if mb.ProviderState != "active" || mb.Connector != ConnectorGoogleUser {
		t.Fatalf("%+v", mb)
	}
	if _, exists := e.fake.Suspended("info@example.org"); !exists {
		t.Fatal("mailbox not created in Google")
	}
	if !e.fake.Deliver("info@example.org", customerMail("info@example.org", "Tickets", "g1@example.com")) {
		t.Fatal("not delivered")
	}
	n, err := e.svc.fetchMailbox(ctx, mb, zerolog.Nop())
	if err != nil || n != 1 {
		t.Fatalf("fetch: %d %v", n, err)
	}
	if n, _ := e.svc.fetchMailbox(ctx, mb, zerolog.Nop()); n != 0 {
		t.Fatal("handled mail must not be fetched again (Desk label)")
	}
	var convID uuid.UUID
	var providerID string
	_ = e.svc.pool.QueryRow(ctx, `SELECT conversation_id, provider_message_id FROM threads WHERE message_id = 'g1@example.com'`).Scan(&convID, &providerID)
	if providerID == "" || !e.fake.Labels("info@example.org", providerID)["Desk"] {
		t.Fatalf("provider id %q not stored or not labelled", providerID)
	}

	c, _ := e.svc.GetConversation(ctx, convID)
	if _, err := e.svc.AddThread(ctx, c, e.anna, ThreadInput{Kind: "reply", Body: "Here you go"}); err != nil {
		t.Fatal(err)
	}
	dueNow(t, e.svc)
	e.svc.sendDue(ctx, zerolog.Nop())
	if len(e.fake.Sent) != 1 || e.fake.Sent[0].User != "info@example.org" || e.fake.Sent[0].From != "info@example.org" {
		t.Fatalf("sent %+v", e.fake.Sent)
	}
	var status, sentID string
	_ = e.svc.pool.QueryRow(ctx, `SELECT send_status, provider_message_id FROM threads WHERE kind = 'reply'`).Scan(&status, &sentID)
	if status != "sent" || sentID != e.fake.Sent[0].ID {
		t.Fatalf("reply %s %s", status, sentID)
	}
}

func TestGoogleGroupMailboxesRouteByRecipient(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	info := e.mailbox(t, ConnectorGoogleGroup, "info@example.org")
	press := e.mailbox(t, ConnectorGoogleGroup, "press@example.org")
	e.fake.Deliver("info@example.org", customerMail("info@example.org", "To info", "r1@example.com"))
	e.fake.Deliver("press@example.org", customerMail("Press <press@example.org>", "To press", "r2@example.com"))
	e.fake.Deliver(shared, customerMail("someone-else@example.org", "Nobody", "r3@example.com"))

	e.svc.fetchShared(ctx, zerolog.Nop())
	for mb, subject := range map[uuid.UUID]string{info.ID: "To info", press.ID: "To press"} {
		var got string
		if err := e.svc.pool.QueryRow(ctx, `SELECT subject FROM conversations WHERE mailbox_id = $1`, mb).Scan(&got); err != nil || got != subject {
			t.Fatalf("mailbox %s: %q %v", mb, got, err)
		}
	}
	st := e.svc.sharedStatus(ctx)
	if st.Unrouted != 1 || st.Error != "" || st.LastFetchAt == nil {
		t.Fatalf("status %+v", st)
	}
	if ids, _ := e.fake.ListUnhandled(ctx, shared, "Desk", 10); len(ids) != 0 {
		t.Fatalf("unhandled left: %v", ids)
	}

	var convID uuid.UUID
	_ = e.svc.pool.QueryRow(ctx, `SELECT id FROM conversations WHERE mailbox_id = $1`, info.ID).Scan(&convID)
	c, _ := e.svc.GetConversation(ctx, convID)
	if _, err := e.svc.AddThread(ctx, c, e.anna, ThreadInput{Kind: "reply", Body: "Hi"}); err != nil {
		t.Fatal(err)
	}
	dueNow(t, e.svc)
	e.svc.sendDue(ctx, zerolog.Nop())
	if len(e.fake.Sent) != 1 || e.fake.Sent[0].User != shared || e.fake.Sent[0].From != "info@example.org" {
		t.Fatalf("group reply must go out from the shared account as the group address: %+v", e.fake.Sent)
	}
}

func TestGoogleValidationAndProvisionRetry(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	_, err := e.svc.CreateMailbox(ctx, e.org, MailboxInput{Connector: ptr(ConnectorGoogleUser), Name: ptr("x"), Email: ptr("x@other.org")})
	validationField(t, err, "email")
	_, err = e.svc.CreateMailbox(ctx, e.org, MailboxInput{Connector: ptr(ConnectorGoogleUser), Name: ptr("x"), Email: ptr("x@example.org"), IMAPHost: ptr("imap.example.org")})
	validationField(t, err, "imap_host")
	_, err = e.svc.CreateMailbox(ctx, e.org, MailboxInput{Connector: ptr(ConnectorGoogleGroup), Name: ptr("x"), Email: ptr(shared)})
	validationField(t, err, "email")

	e.fake.FailNext["CreateMailbox"] = errors.New("quota exceeded")
	mb := e.mailbox(t, ConnectorGoogleUser, "sales@example.org")
	if mb.ProviderState != "error" || !strings.Contains(mb.ProviderError, "quota exceeded") {
		t.Fatalf("%+v", mb)
	}
	mb, err = e.svc.Provision(ctx, mb)
	if err != nil || mb.ProviderState != "active" || mb.ProviderError != "" {
		t.Fatalf("retry: %+v %v", mb, err)
	}

	noGoogle := NewService(e.svc.pool, Options{Keyring: keyring(t, "k"), Development: true})
	_, err = noGoogle.CreateMailbox(ctx, e.org, MailboxInput{Connector: ptr(ConnectorGoogleUser), Name: ptr("y"), Email: ptr("y@example.org")})
	validationField(t, err, "connector")
}

func TestMailboxTrashRestorePurge(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	mb := e.mailbox(t, ConnectorGoogleUser, "lost@example.org")
	e.fake.Deliver("lost@example.org", customerMail("lost@example.org", "My bag", "p1@example.com"))
	if _, err := e.svc.fetchMailbox(ctx, mb, zerolog.Nop()); err != nil {
		t.Fatal(err)
	}
	if len(e.blobs.Keys()) == 0 {
		t.Fatal("raw mail should be stored")
	}

	mb, err := e.svc.TrashMailbox(ctx, mb.ID, e.anna)
	if err != nil {
		t.Fatal(err)
	}
	if mb.DeletedAt == nil || mb.PurgeAfter == nil || mb.ProviderState != "suspended" {
		t.Fatalf("%+v", mb)
	}
	if time.Until(*mb.PurgeAfter) < 29*24*time.Hour {
		t.Fatalf("default purge period is 30 days: %s", mb.PurgeAfter)
	}
	if suspended, _ := e.fake.Suspended("lost@example.org"); !suspended {
		t.Fatal("Google mailbox not suspended")
	}
	list, _ := e.svc.ListMailboxes(ctx, NewAccess(e.anna.ID, nil, []uuid.UUID{e.org}))
	if len(list) != 0 {
		t.Fatal("trashed mailboxes leave the sidebar")
	}

	if _, err := e.svc.RestoreMailbox(ctx, mb.ID, e.anna); err != nil {
		t.Fatal(err)
	}
	if suspended, _ := e.fake.Suspended("lost@example.org"); suspended {
		t.Fatal("restore must resume the Google mailbox")
	}
	if err := e.svc.PurgeMailbox(ctx, mb.ID, e.anna); err == nil {
		t.Fatal("purging a mailbox outside the trash must fail")
	}
	if _, err := e.svc.TrashMailbox(ctx, mb.ID, e.anna); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.PurgeMailbox(ctx, mb.ID, e.anna); err != nil {
		t.Fatal(err)
	}
	if _, exists := e.fake.Suspended("lost@example.org"); exists {
		t.Fatal("purge must delete the Google mailbox")
	}
	if len(e.blobs.Keys()) != 0 {
		t.Fatalf("files left: %v", e.blobs.Keys())
	}
	entries, _ := e.svc.AuditLog(ctx, AuditFilter{})
	var actions []string
	for _, a := range entries {
		actions = append(actions, a.Action)
	}
	if strings.Join(actions, ",") != "mailbox.purge,mailbox.trash,mailbox.restore,mailbox.trash" {
		t.Fatalf("audit %v", actions)
	}
}

func TestPurgeRemindersAndDailyPurge(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	if _, err := e.svc.pool.Exec(ctx, `UPDATE users SET desk_admin = true WHERE id = $1`, e.anna.ID); err != nil {
		t.Fatal(err)
	}
	mb := e.mailbox(t, ConnectorGoogleUser, "old@example.org")
	if _, err := e.svc.TrashMailbox(ctx, mb.ID, e.anna); err != nil {
		t.Fatal(err)
	}
	outbox := func() int {
		var n int
		_ = e.svc.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE to_email = $1`, e.anna.Email).Scan(&n)
		return n
	}
	now := time.Now()
	e.svc.purgeDueMailboxes(ctx, now, zerolog.Nop())
	if outbox() != 0 {
		t.Fatal("no reminder 30 days ahead")
	}
	purgeAt := now.Add(6 * 24 * time.Hour)
	_, _ = e.svc.pool.Exec(ctx, `UPDATE mailboxes SET purge_after = $2 WHERE id = $1`, mb.ID, purgeAt)
	e.svc.purgeDueMailboxes(ctx, now, zerolog.Nop())
	e.svc.purgeDueMailboxes(ctx, now, zerolog.Nop())
	if outbox() != 1 {
		t.Fatalf("one reminder 7 days ahead, got %d", outbox())
	}
	e.svc.purgeDueMailboxes(ctx, purgeAt.Add(-12*time.Hour), zerolog.Nop())
	if outbox() != 2 {
		t.Fatalf("second reminder 1 day ahead, got %d", outbox())
	}
	var subject string
	_ = e.svc.pool.QueryRow(ctx, `SELECT subject FROM outbox LIMIT 1`).Scan(&subject)
	if !strings.Contains(subject, "old@example.org") {
		t.Fatalf("subject %q", subject)
	}
	e.svc.purgeDueMailboxes(ctx, purgeAt.Add(time.Minute), zerolog.Nop())
	if _, err := e.svc.GetMailbox(ctx, mb.ID); err == nil {
		t.Fatal("mailbox should be purged")
	}
}

func TestDenRetentionOverridesDesk(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	v, err := e.svc.RetentionSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if *v.Effective.MailboxTrashDays != 30 || *v.Effective.TrashDays != 30 || len(v.PendingLegalReview) != 3 {
		t.Fatalf("defaults %+v", v)
	}
	if _, err := e.svc.SetRetention(ctx, Retention{ClosedConversationDays: ptr(730), TrashDays: ptr(0)}, e.anna); err == nil {
		t.Fatal("0 days must be refused")
	}
	if _, err := e.svc.SetRetention(ctx, Retention{ClosedConversationDays: ptr(730)}, e.anna); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.pool.Exec(ctx, `INSERT INTO den_link (den_base_url, credential, bundle) VALUES ('https://den', 'x', $1)`,
		[]byte(`{"retention":{"desk":{"mailbox_trash_days":10,"raw_mail_days":90}}}`)); err != nil {
		t.Fatal(err)
	}
	v, _ = e.svc.RetentionSettings(ctx)
	if *v.Effective.MailboxTrashDays != 10 || *v.Effective.RawMailDays != 90 || *v.Effective.ClosedConversationDays != 730 {
		t.Fatalf("effective %+v", v.Effective)
	}
	if v.ManagedBy["raw_mail_days"] != "den" || v.ManagedBy["closed_conversation_days"] != "" {
		t.Fatalf("managed by %v", v.ManagedBy)
	}
	_, err = e.svc.SetRetention(ctx, Retention{RawMailDays: ptr(30)}, e.anna)
	validationField(t, err, "raw_mail_days")
}

func TestOutboxDelivery(t *testing.T) {
	port, got := fakeSMTP(t)
	pool := testdb.New(t)
	svc := NewService(pool, Options{
		Keyring: keyring(t, "k"), Development: true, NotifyFrom: "Desk <desk@desk.test>",
		SMTP: SMTPSettings{Host: "127.0.0.1", Port: port, Security: "none"},
	})
	ctx := context.Background()
	if err := svc.queueEmail(ctx, pool, []string{"admin@desk.test"}, "Hello", "Body"); err != nil {
		t.Fatal(err)
	}
	svc.sendOutbox(ctx, zerolog.Nop())
	select {
	case msg := <-got:
		if !strings.Contains(msg, "Auto-Submitted: auto-generated") || !strings.Contains(msg, "Body") {
			t.Fatalf("message %q", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("not sent")
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM outbox`).Scan(&status)
	if status != "sent" {
		t.Fatalf("status %s", status)
	}
	if !svc.NotificationsEnabled() {
		t.Fatal("notifications should be enabled")
	}
}
