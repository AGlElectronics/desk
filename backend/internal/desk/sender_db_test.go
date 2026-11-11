package desk

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/domain"
	"github.com/AGlElectronics/desk/backend/internal/testdb"
)

func agent(t *testing.T, pool *pgxpool.Pool, name string) *Person {
	t.Helper()
	p := &Person{DisplayName: name, Email: strings.ToLower(name) + "@desk.test"}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (issuer, subject, display_name, email) VALUES ('test', $1, $2, $3) RETURNING id`,
		uuid.NewString(), p.DisplayName, p.Email).Scan(&p.ID); err != nil {
		t.Fatal(err)
	}
	return p
}

func senderService(t *testing.T, smtpHost string, smtpPort int) (*Service, Mailbox, *Person) {
	t.Helper()
	pool := testdb.New(t)
	svc := NewService(pool, Options{
		Keyring: keyring(t, "k"), Development: true,
		SMTP: SMTPSettings{Host: smtpHost, Port: smtpPort, Security: "none"},
	})
	mb, err := svc.CreateMailbox(context.Background(), uuid.MustParse(testdb.DemoOrg), MailboxInput{
		Name: ptr("Support"), Email: ptr("support-" + uuid.NewString()[:6] + "@desk.test"), Signature: ptr("-- {agent.name}"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, mb, agent(t, pool, "Anna")
}

func dueNow(t *testing.T, svc *Service) {
	t.Helper()
	if _, err := svc.pool.Exec(context.Background(), `UPDATE threads SET next_attempt_at = now() - interval '1 second' WHERE send_status = 'queued'`); err != nil {
		t.Fatal(err)
	}
}

func sendStatus(t *testing.T, svc *Service, id uuid.UUID) (status string, attempts int, next *time.Time) {
	t.Helper()
	if err := svc.pool.QueryRow(context.Background(), `SELECT send_status, send_attempts, next_attempt_at FROM threads WHERE id = $1`, id).Scan(&status, &attempts, &next); err != nil {
		t.Fatal(err)
	}
	return
}

func TestCreateConversationIsAtomic(t *testing.T) {
	svc, mb, anna := senderService(t, "localhost", 1)
	ctx := context.Background()
	_, err := svc.CreateConversation(ctx, anna, NewConversationInput{
		MailboxID: mb.ID, CustomerEmail: "alice@example.com", Subject: "Hi", Body: "Hello", CC: []string{"not an address"},
	})
	if err == nil {
		t.Fatal("expected a validation error")
	}
	var n int
	_ = svc.pool.QueryRow(ctx, `SELECT count(*) FROM conversations`).Scan(&n)
	if n != 0 {
		t.Fatal("a failed first reply must not leave an empty conversation")
	}
	c, err := svc.CreateConversation(ctx, anna, NewConversationInput{
		MailboxID: mb.ID, CustomerEmail: "Alice <alice@example.com>", Subject: "Hi", BodyHTML: "<p>Hello <b>Alice</b></p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.ThreadCount != 1 || c.Customer.Name != "Alice" || c.Status != domain.StatusPending {
		t.Fatalf("%+v", c)
	}
}

func TestReplyAllDefaultCC(t *testing.T) {
	svc, mb, anna := senderService(t, "localhost", 1)
	ctx := context.Background()
	raw := []byte("From: alice@example.com\r\nTo: " + mb.Email + ", bob@example.com\r\nCc: carol@example.com, Alice@example.com\r\nSubject: Q\r\nMessage-ID: <cc1@example.com>\r\n\r\nHi\r\n")
	res := mustImport(t, svc, mb, raw)
	c, _ := svc.GetConversation(ctx, res.ConversationID)
	id, err := svc.AddThread(ctx, c, anna, ThreadInput{Kind: "reply", Body: "Answer"})
	if err != nil {
		t.Fatal(err)
	}
	var to, cc []string
	var text string
	_ = svc.pool.QueryRow(ctx, `SELECT to_emails, cc_emails, body_text FROM threads WHERE id = $1`, id).Scan(&to, &cc, &text)
	if strings.Join(to, ",") != "alice@example.com" || strings.Join(cc, ",") != "bob@example.com,carol@example.com" {
		t.Fatalf("to %v cc %v", to, cc)
	}
	if !strings.HasSuffix(text, "-- Anna") {
		t.Fatalf("signature missing: %q", text)
	}
	none := []string{}
	id, _ = svc.AddThread(ctx, c, anna, ThreadInput{Kind: "reply", Body: "Only Alice", CC: &none})
	_ = svc.pool.QueryRow(ctx, `SELECT cc_emails FROM threads WHERE id = $1`, id).Scan(&cc)
	if len(cc) != 0 {
		t.Fatalf("explicit empty CC: %v", cc)
	}
}

func TestClaimSkipsLockedRows(t *testing.T) {
	svc, mb, anna := senderService(t, "localhost", 1)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := svc.CreateConversation(ctx, anna, NewConversationInput{MailboxID: mb.ID, CustomerEmail: "c@example.com", Subject: "s", Body: "b"}); err != nil {
			t.Fatal(err)
		}
	}
	dueNow(t, svc)
	var wg sync.WaitGroup
	got := make([][]uuid.UUID, 4)
	for i := range got {
		wg.Go(func() {
			ids, err := svc.claimDue(ctx, 5)
			if err != nil {
				t.Error(err)
			}
			got[i] = ids
		})
	}
	wg.Wait()
	seen := map[uuid.UUID]bool{}
	total := 0
	for _, ids := range got {
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("reply %s claimed twice", id)
			}
			seen[id] = true
			total++
		}
	}
	if total != 10 {
		t.Fatalf("claimed %d of 10", total)
	}
}

func TestRetryStateMachine(t *testing.T) {
	// Port 1 on loopback: nothing listens, every attempt fails.
	svc, mb, anna := senderService(t, "127.0.0.1", 1)
	ctx := context.Background()
	c, err := svc.CreateConversation(ctx, anna, NewConversationInput{MailboxID: mb.ID, CustomerEmail: "c@example.com", Subject: "s", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	_ = svc.pool.QueryRow(ctx, `SELECT id FROM threads WHERE conversation_id = $1 AND kind = 'reply'`, c.ID).Scan(&id)

	for attempt := 1; attempt <= len(retryDelays); attempt++ {
		dueNow(t, svc)
		svc.sendDue(ctx, zerolog.Nop())
		status, attempts, next := sendStatus(t, svc, id)
		if status != "queued" || attempts != attempt || next == nil || time.Until(*next) < retryDelays[attempt-1]-5*time.Second {
			t.Fatalf("attempt %d: status=%s attempts=%d next=%v", attempt, status, attempts, next)
		}
	}
	dueNow(t, svc)
	svc.sendDue(ctx, zerolog.Nop())
	if status, attempts, next := sendStatus(t, svc, id); status != "failed" || attempts != len(retryDelays)+1 || next != nil {
		t.Fatalf("after the last attempt: %s %d %v", status, attempts, next)
	}
	var sendErr string
	_ = svc.pool.QueryRow(ctx, `SELECT send_error FROM threads WHERE id = $1`, id).Scan(&sendErr)
	if strings.Contains(sendErr, "127.0.0.1") {
		t.Fatalf("send error leaks the address: %q", sendErr)
	}
	if err := svc.RetryReply(ctx, id); err != nil {
		t.Fatal(err)
	}
	if status, attempts, _ := sendStatus(t, svc, id); status != "queued" || attempts != 0 {
		t.Fatalf("after retry: %s %d", status, attempts)
	}
}

func TestStaleClaimReturnsToQueue(t *testing.T) {
	svc, mb, anna := senderService(t, "localhost", 1)
	ctx := context.Background()
	c, _ := svc.CreateConversation(ctx, anna, NewConversationInput{MailboxID: mb.ID, CustomerEmail: "c@example.com", Subject: "s", Body: "b"})
	if _, err := svc.pool.Exec(ctx, `UPDATE threads SET send_status = 'sending', claimed_at = now() - interval '1 hour', next_attempt_at = now() WHERE conversation_id = $1 AND kind = 'reply'`, c.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := svc.claimDue(ctx, 5)
	if err != nil || len(ids) != 1 {
		t.Fatalf("stale claim not reclaimed: %v %v", ids, err)
	}
}

// Real delivery through GreenMail; the message is read back over IMAP.
func TestSendThroughGreenMail(t *testing.T) {
	host := greenmail(t)
	svc, mb, anna := senderService(t, host, 3025)
	ctx := context.Background()
	customer := "cust-" + uuid.NewString()[:8] + "@desk.test"
	c, err := svc.CreateConversation(ctx, anna, NewConversationInput{
		MailboxID: mb.ID, CustomerEmail: customer, Subject: "Your badge", BodyHTML: "<p>It is <b>ready</b></p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	dueNow(t, svc)
	svc.sendDue(ctx, zerolog.Nop())
	var id uuid.UUID
	_ = svc.pool.QueryRow(ctx, `SELECT id FROM threads WHERE conversation_id = $1 AND kind = 'reply'`, c.ID).Scan(&id)
	if status, _, _ := sendStatus(t, svc, id); status != "sent" {
		var e string
		_ = svc.pool.QueryRow(ctx, `SELECT send_error FROM threads WHERE id = $1`, id).Scan(&e)
		t.Fatalf("status %s (%s)", status, e)
	}

	cl, err := imapclient.DialInsecure(host+":3143", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if err := cl.Login(customer, customer).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	section := &imap.FetchItemBodySection{}
	msgs, err := cl.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("fetch: %v (%d)", err, len(msgs))
	}
	p := parse(t, msgs[0].FindBodySection(section))
	if !strings.Contains(p.Subject, "[#"+strconv.Itoa(c.Number)+"]") || !strings.Contains(p.HTML, "<b>ready</b>") || !strings.Contains(p.Text, "ready") {
		t.Fatalf("received %q / %q / %q", p.Subject, p.HTML, p.Text)
	}
}
