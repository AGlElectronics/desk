package desk

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AGlElectronics/desk/backend/internal/blob"
	"github.com/AGlElectronics/desk/backend/internal/testdb"
)

func importService(t *testing.T) (*Service, *blob.Memory, Mailbox) {
	t.Helper()
	pool := testdb.New(t)
	blobs := blob.NewMemory()
	svc := NewService(pool, Options{Keyring: keyring(t, "k"), Development: true, Blobs: blobs})
	mb, err := svc.CreateMailbox(context.Background(), uuid.MustParse(testdb.DemoOrg), MailboxInput{Name: ptr("Support"), Email: ptr("support@desk.test")})
	if err != nil {
		t.Fatal(err)
	}
	return svc, blobs, mb
}

func mustImport(t *testing.T, svc *Service, mb Mailbox, raw []byte) ImportResult {
	t.Helper()
	res, err := svc.Import(context.Background(), mb, parse(t, raw), raw)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestImportStoresFilesInObjectStorage(t *testing.T) {
	svc, blobs, mb := importService(t)
	ctx := context.Background()
	raw := fixture(t, "outlook_inline_image.eml")
	res := mustImport(t, svc, mb, raw)
	if !res.Created {
		t.Fatalf("%+v", res)
	}
	if len(blobs.Keys()) != 2 {
		t.Fatalf("expected attachment and raw message in storage, got %v", blobs.Keys())
	}
	c, _ := svc.GetConversation(ctx, res.ConversationID)
	d, err := svc.Detail(ctx, NewAccess(uuid.Nil, nil, []uuid.UUID{mb.OrganizationID}), c, mb)
	if err != nil {
		t.Fatal(err)
	}
	th := d.Threads[0]
	if len(th.Attachments) != 1 || !th.Attachments[0].Inline || th.Attachments[0].ContentID == "" {
		t.Fatalf("attachment meta %+v", th.Attachments)
	}
	if !strings.Contains(th.BodyHTML, AttachmentURL(th.Attachments[0].ID)) {
		t.Fatalf("inline image not linked: %s", th.BodyHTML)
	}
	_, _, key, err := svc.AttachmentInfo(ctx, th.Attachments[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	r, size, err := svc.OpenAttachment(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	if int64(len(b)) != size || !strings.HasPrefix(string(b), "\x89PNG") {
		t.Fatalf("content %q", b)
	}
	var rawKey string
	_ = svc.pool.QueryRow(ctx, `SELECT raw_key FROM threads WHERE id = $1`, th.ID).Scan(&rawKey)
	if rr, _, err := blobs.Get(ctx, rawKey); err != nil {
		t.Fatalf("raw message: %v", err)
	} else if b, _ := io.ReadAll(rr); string(b) != string(raw) {
		t.Fatal("raw message differs")
	}

	// Same Message-ID again: nothing stored, nothing left in storage.
	if res := mustImport(t, svc, mb, raw); !res.Duplicate {
		t.Fatalf("%+v", res)
	}
	if len(blobs.Keys()) != 2 {
		t.Fatalf("duplicate left files behind: %v", blobs.Keys())
	}
}

func TestImportStorageFailureIsRetried(t *testing.T) {
	svc, blobs, mb := importService(t)
	blobs.FailPut = "raw/"
	raw := fixture(t, "outlook_inline_image.eml")
	if _, err := svc.Import(context.Background(), mb, parse(t, raw), raw); err == nil {
		t.Fatal("expected an error so the fetcher retries")
	}
	if len(blobs.Keys()) != 0 {
		t.Fatalf("attachment upload not rolled back: %v", blobs.Keys())
	}
	blobs.FailPut = ""
	if res := mustImport(t, svc, mb, raw); !res.Created {
		t.Fatalf("retry: %+v", res)
	}
}

func TestAutomaticMailNeverOpensOrReopens(t *testing.T) {
	svc, _, mb := importService(t)
	ctx := context.Background()
	if res := mustImport(t, svc, mb, fixture(t, "bounce.eml")); res.Skipped == "" {
		t.Fatalf("a bounce without conversation must be skipped: %+v", res)
	}

	first := mustImport(t, svc, mb, []byte("From: maria.example@example.com\r\nTo: support@desk.test\r\nSubject: Hi\r\nMessage-ID: <q1@example.com>\r\n\r\nQuestion\r\n"))
	if _, err := svc.pool.Exec(ctx, `UPDATE conversations SET status = 'closed' WHERE id = $1`, first.ConversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pool.Exec(ctx, `INSERT INTO message_ids (message_id, conversation_id) VALUES ('desk.reply-1@desk.test', $1)`, first.ConversationID); err != nil {
		t.Fatal(err)
	}
	res := mustImport(t, svc, mb, fixture(t, "out_of_office.eml"))
	if res.ConversationID != first.ConversationID || res.Created {
		t.Fatalf("auto-reply should join the conversation: %+v", res)
	}
	c, _ := svc.GetConversation(ctx, first.ConversationID)
	if c.Status != "closed" {
		t.Fatalf("auto-reply reopened the conversation: %s", c.Status)
	}
	var auto bool
	_ = svc.pool.QueryRow(ctx, `SELECT auto_submitted FROM threads WHERE message_id = 'ooo-4711@example.com'`).Scan(&auto)
	if !auto {
		t.Fatal("thread not flagged as automatic")
	}
}

func TestSubjectNumberNeedsTheCustomer(t *testing.T) {
	svc, _, mb := importService(t)
	first := mustImport(t, svc, mb, []byte("From: alice@example.com\r\nTo: support@desk.test\r\nSubject: Hotel\r\nMessage-ID: <a1@example.com>\r\n\r\nHi\r\n"))
	c, _ := svc.GetConversation(context.Background(), first.ConversationID)
	subject := "Re: Hotel [#" + strconv.Itoa(c.Number) + "]"

	stranger := mustImport(t, svc, mb, []byte("From: mallory@example.net\r\nTo: support@desk.test\r\nSubject: "+subject+"\r\nMessage-ID: <m1@example.net>\r\n\r\nLet me in\r\n"))
	if stranger.ConversationID == first.ConversationID || !stranger.Created {
		t.Fatalf("a stranger must not join by subject number: %+v", stranger)
	}
	customer := mustImport(t, svc, mb, []byte("From: Alice@Example.com\r\nTo: support@desk.test\r\nSubject: "+subject+"\r\nMessage-ID: <a2@example.com>\r\n\r\nMore\r\n"))
	if customer.ConversationID != first.ConversationID {
		t.Fatalf("the customer should thread by subject number: %+v", customer)
	}
}

func TestImportOfNULBytesSucceeds(t *testing.T) {
	svc, _, mb := importService(t)
	raw := []byte("From: a@example.com\r\nTo: support@desk.test\r\nSubject: nul\r\nMessage-ID: <nul@example.com>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello\x00world \xc3\x28\r\n")
	if res := mustImport(t, svc, mb, raw); !res.Created {
		t.Fatalf("%+v", res)
	}
}
