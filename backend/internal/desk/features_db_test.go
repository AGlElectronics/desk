package desk

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

func (e googleEnv) admin() Access { return NewAccess(e.anna.ID, nil, []uuid.UUID{e.org}) }

func (e googleEnv) deliverAndFetch(t *testing.T, mb Mailbox, raw []byte) ImportResult {
	t.Helper()
	res, err := e.svc.Import(context.Background(), mb, parse(t, raw), raw)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func mailFrom(from, to, subject, extra string) []byte {
	return []byte("From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nMessage-ID: <" + uuid.NewString() + "@example.com>\r\n" + extra + "\r\nHello there, I lost my umbrella at the hotel\r\n")
}

func (e googleEnv) list(t *testing.T, f ListFilter) ListPage {
	t.Helper()
	if f.MailboxIDs == nil {
		boxes, _ := e.svc.VisibleMailboxes(context.Background(), e.admin())
		for _, b := range boxes {
			f.MailboxIDs = append(f.MailboxIDs, b.ID)
		}
	}
	f.UserID = e.anna.ID
	page, err := e.svc.ListConversations(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestTagsStarsPaginationAndSearch(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	mb := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	var ids []uuid.UUID
	for i := 0; i < 7; i++ {
		res := e.deliverAndFetch(t, mb, mailFrom(fmt.Sprintf("c%d@example.com", i), mb.Email, fmt.Sprintf("Question %d", i), ""))
		ids = append(ids, res.ConversationID)
		time.Sleep(2 * time.Millisecond)
	}

	seen := map[uuid.UUID]bool{}
	page := e.list(t, ListFilter{Folder: "all", Limit: 3})
	pages := 1
	for {
		for _, c := range page.Conversations {
			if seen[c.ID] {
				t.Fatalf("conversation %s on two pages", c.ID)
			}
			seen[c.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		page = e.list(t, ListFilter{Folder: "all", Limit: 3, Cursor: page.NextCursor})
		pages++
	}
	if len(seen) != 7 || pages != 3 {
		t.Fatalf("saw %d conversations on %d pages", len(seen), pages)
	}

	c, _ := e.svc.GetConversation(ctx, ids[2])
	tags := []string{"Lost & Found"}
	if err := e.svc.UpdateConversation(ctx, c, mb, e.anna, UpdateInput{Tags: &tags}); err == nil {
		t.Fatal("& is not allowed in tags")
	}
	tags = []string{"VIP", " lost  found ", "vip"}
	if err := e.svc.UpdateConversation(ctx, c, mb, e.anna, UpdateInput{Tags: &tags}); err != nil {
		t.Fatal(err)
	}
	c, _ = e.svc.GetConversation(ctx, ids[2])
	if strings.Join(c.Tags, ",") != "lost found,vip" {
		t.Fatalf("tags %v", c.Tags)
	}
	if got := e.list(t, ListFilter{Folder: "all", Tag: "VIP"}).Conversations; len(got) != 1 || got[0].ID != c.ID {
		t.Fatalf("tag filter %v", got)
	}
	if all, _ := e.svc.Tags(ctx, []uuid.UUID{mb.ID}); strings.Join(all, ",") != "lost found,vip" {
		t.Fatalf("tag list %v", all)
	}

	if err := e.svc.SetStar(ctx, ids[4], e.anna.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := e.list(t, ListFilter{Folder: "starred"}).Conversations; len(got) != 1 || !got[0].Starred {
		t.Fatalf("starred %v", got)
	}
	counts, _ := e.svc.MailboxCounts(ctx, mb, e.anna.ID)
	if counts.Starred != 1 || counts.Unassigned != 7 {
		t.Fatalf("counts %+v", counts)
	}

	for q, want := range map[string]int{"umbrella": 7, "hotel umbrella": 7, "c3@example.com": 1, "Question": 7, "#" + strconv.Itoa(c.Number): 1, "nonexistentword": 0} {
		if got := len(e.list(t, ListFilter{Folder: "all", Query: q}).Conversations); got != want {
			t.Errorf("search %q: %d results, want %d", q, got, want)
		}
	}
}

func TestSavedReplies(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	info := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	press := e.mailbox(t, ConnectorGoogleUser, "press@example.org")
	if _, err := e.svc.CreateSavedReply(ctx, SavedReplyInput{Name: "", BodyHTML: "<p>x</p>"}, e.anna); err == nil {
		t.Fatal("name required")
	}
	global, err := e.svc.CreateSavedReply(ctx, SavedReplyInput{Name: "Thanks", BodyHTML: "<p>Thank <b>you</b></p><script>x</script>"}, e.anna)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(global.BodyHTML, "script") || global.BodyText != "Thank you" {
		t.Fatalf("%+v", global)
	}
	if _, err := e.svc.CreateSavedReply(ctx, SavedReplyInput{MailboxID: &info.ID, Name: "Opening hours", BodyText: "9 to 5"}, e.anna); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.SavedReplies(ctx, info.ID); len(got) != 2 {
		t.Fatalf("info has %d", len(got))
	}
	if got, _ := e.svc.SavedReplies(ctx, press.ID); len(got) != 1 {
		t.Fatalf("press has %d", len(got))
	}
	upd, err := e.svc.UpdateSavedReply(ctx, global.ID, SavedReplyInput{Name: "Thanks!", BodyText: "Cheers"})
	if err != nil || upd.Name != "Thanks!" || upd.BodyText != "Cheers" {
		t.Fatalf("%+v %v", upd, err)
	}
	_ = e.svc.DeleteSavedReply(ctx, global.ID)
	if got, _ := e.svc.SavedReplies(ctx, press.ID); len(got) != 0 {
		t.Fatal("not deleted")
	}
}

func TestAutoReplyWithLoopProtection(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	mb := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	if _, err := e.svc.UpdateMailbox(ctx, mb.ID, MailboxInput{AutoReply: &AutoReply{Enabled: true}}); err == nil {
		t.Fatal("an enabled auto-reply needs a body")
	}
	mb, err := e.svc.UpdateMailbox(ctx, mb.ID, MailboxInput{AutoReply: &AutoReply{Enabled: true, Subject: "We got your message", BodyHTML: "<p>Thanks, we answer within a day.</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	autoReplies := func() int {
		var n int
		_ = e.svc.pool.QueryRow(ctx, `SELECT count(*) FROM threads WHERE kind = 'auto_reply'`).Scan(&n)
		return n
	}
	e.deliverAndFetch(t, mb, mailFrom("alice@example.com", mb.Email, "Hotel", ""))
	if autoReplies() != 1 {
		t.Fatal("first message gets an auto-reply")
	}
	e.deliverAndFetch(t, mb, mailFrom("alice@example.com", mb.Email, "Another topic", ""))
	e.deliverAndFetch(t, mb, mailFrom("bot@example.com", mb.Email, "Out of office", "Auto-Submitted: auto-replied\r\n"))
	e.deliverAndFetch(t, mb, mailFrom("noreply@shop.example", mb.Email, "Your order", ""))
	e.deliverAndFetch(t, mb, mailFrom("list@example.com", mb.Email, "Newsletter", "List-Id: <news.example.com>\r\n"))
	if n := autoReplies(); n != 1 {
		t.Fatalf("loop protection failed: %d auto-replies", n)
	}

	_, _ = e.svc.pool.Exec(ctx, `UPDATE threads SET next_attempt_at = now() - interval '1 second' WHERE send_status = 'queued'`)
	e.svc.sendDue(ctx, zerolog.Nop())
	if len(e.fake.Sent) != 1 {
		t.Fatalf("sent %d", len(e.fake.Sent))
	}
	raw := string(e.fake.Sent[0].Raw)
	if !strings.Contains(raw, "Auto-Submitted: auto-replied") || !strings.Contains(raw, "We got your message [#1]") {
		t.Fatalf("auto-reply message:\n%s", raw)
	}
}

func TestForwardMergeMove(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	info := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	press := e.mailbox(t, ConnectorGoogleUser, "press@example.org")
	raw := []byte("From: alice@example.com\r\nTo: info@example.org\r\nSubject: Photo\r\nMessage-ID: <fw1@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\nSee photo\r\n--B\r\nContent-Type: image/png\r\nContent-Disposition: attachment; filename=\"p.png\"\r\n\r\nPNGDATA\r\n--B--\r\n")
	a := e.deliverAndFetch(t, info, raw)
	c, _ := e.svc.GetConversation(ctx, a.ConversationID)
	if _, err := e.svc.Forward(ctx, c, e.anna, ForwardInput{To: []string{"photographer@example.net"}, Body: "Can you print this?"}); err != nil {
		t.Fatal(err)
	}
	_, _ = e.svc.pool.Exec(ctx, `UPDATE threads SET next_attempt_at = now() - interval '1 second' WHERE send_status = 'queued'`)
	e.svc.sendDue(ctx, zerolog.Nop())
	if len(e.fake.Sent) != 1 {
		t.Fatal("forward not sent")
	}
	fw := parse(t, e.fake.Sent[0].Raw)
	if fw.Subject != "Fwd: Photo" || len(fw.Attachments) != 1 || fw.Attachments[0].Filename != "p.png" || !strings.Contains(fw.Text, "Can you print this?") || !strings.Contains(fw.Text, "See photo") {
		t.Fatalf("forward %q %q %+v", fw.Subject, fw.Text, fw.Attachments)
	}
	if strings.Join(fw.To, ",") != "photographer@example.net" {
		t.Fatalf("forward to %v", fw.To)
	}

	b := e.deliverAndFetch(t, info, mailFrom("alice@example.com", info.Email, "Photo again", ""))
	src, _ := e.svc.GetConversation(ctx, b.ConversationID)
	dst, _ := e.svc.GetConversation(ctx, a.ConversationID)
	if err := e.svc.MergeConversations(ctx, src, dst, info, info, e.anna); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetConversation(ctx, src.ID); err == nil {
		t.Fatal("merged conversation should be gone")
	}
	d, _ := e.svc.Detail(ctx, e.admin(), dst, info)
	if d.ThreadCount < 3 || !strings.Contains(d.Threads[len(d.Threads)-1].BodyText, "Merged") {
		t.Fatalf("after merge: %d threads, last %q", d.ThreadCount, d.Threads[len(d.Threads)-1].BodyText)
	}

	if err := e.svc.MoveConversation(ctx, dst, info, press, e.anna); err != nil {
		t.Fatal(err)
	}
	moved, _ := e.svc.GetConversation(ctx, dst.ID)
	if moved.MailboxID != press.ID || moved.Number != 1 {
		t.Fatalf("moved %+v", moved)
	}
	// A customer answer to the old Message-IDs follows the conversation.
	reply := []byte("From: alice@example.com\r\nTo: press@example.org\r\nSubject: Re: Photo\r\nMessage-ID: <fw2@example.com>\r\nIn-Reply-To: <fw1@example.com>\r\n\r\nthanks\r\n")
	if res := e.deliverAndFetch(t, press, reply); res.ConversationID != dst.ID {
		t.Fatalf("reply after move went to %s", res.ConversationID)
	}
}

func TestDeleteEmailsAndRetention(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	mb := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	e.fake.Deliver(mb.Email, []byte("From: alice@example.com\r\nTo: info@example.org\r\nSubject: Lost bag\r\nMessage-ID: <d1@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\nbag\r\n--B\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"r.pdf\"\r\n\r\n%PDF\r\n--B--\r\n"))
	if _, err := e.svc.fetchMailbox(ctx, mb, zerolog.Nop()); err != nil {
		t.Fatal(err)
	}
	var threadID, convID uuid.UUID
	var providerID string
	_ = e.svc.pool.QueryRow(ctx, `SELECT id, conversation_id, provider_message_id FROM threads WHERE message_id = 'd1@example.com'`).Scan(&threadID, &convID, &providerID)

	if _, err := e.svc.DeleteThread(ctx, mb, threadID, e.anna); err != nil {
		t.Fatal(err)
	}
	if !e.fake.Labels(mb.Email, providerID)["TRASH"] {
		t.Fatal("not in Gmail's trash")
	}
	c, _ := e.svc.GetConversation(ctx, convID)
	viewer := NewAccess(uuid.New(), []string{"desk-info-viewer"}, nil)
	if d, _ := e.svc.Detail(ctx, viewer, c, mb); len(d.Threads) != 0 {
		t.Fatal("viewers do not see deleted emails")
	}
	if d, _ := e.svc.Detail(ctx, e.admin(), c, mb); len(d.Threads) != 1 || d.Threads[0].DeletedAt == nil {
		t.Fatal("the delete role sees deleted emails to restore them")
	}
	if _, err := e.svc.RestoreThread(ctx, mb, threadID, e.anna); err != nil {
		t.Fatal(err)
	}
	if e.fake.Labels(mb.Email, providerID)["TRASH"] {
		t.Fatal("still in Gmail's trash after restore")
	}

	if _, err := e.svc.DeleteConversation(ctx, c, mb, e.anna); err != nil {
		t.Fatal(err)
	}
	if !e.fake.Labels(mb.Email, providerID)["TRASH"] {
		t.Fatal("conversation delete must trash its emails in Gmail")
	}
	if got := e.list(t, ListFilter{Folder: "trash"}).Conversations; len(got) != 1 {
		t.Fatalf("trash folder %v", got)
	}
	if got := e.list(t, ListFilter{Folder: "all"}).Conversations; len(got) != 0 {
		t.Fatal("trashed conversations leave the other folders")
	}
	if n := len(e.blobs.Keys()); n != 2 {
		t.Fatalf("files %d", n)
	}
	// Not old enough yet.
	if res, _ := e.svc.ApplyRetention(ctx, time.Now()); res.total() != 0 {
		t.Fatalf("purged too early: %+v", res)
	}
	res, err := e.svc.ApplyRetention(ctx, time.Now().Add(31*24*time.Hour))
	if err != nil || res.TrashedConversations != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(e.blobs.Keys()) != 0 {
		t.Fatalf("files left %v", e.blobs.Keys())
	}
}

func TestRetentionPeriods(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	mb := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	raw := []byte("From: alice@example.com\r\nTo: info@example.org\r\nSubject: Old\r\nMessage-ID: <old@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\nx\r\n--B\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"r.pdf\"\r\n\r\n%PDF\r\n--B--\r\n")
	old := e.deliverAndFetch(t, mb, raw)
	keep := e.deliverAndFetch(t, mb, mailFrom("bob@example.com", mb.Email, "Open one", ""))
	if res, _ := e.svc.ApplyRetention(ctx, time.Now().Add(10*365*24*time.Hour)); res.ClosedConversations+res.Attachments+res.RawMessages != 0 {
		t.Fatalf("defaults are pending legal review and keep everything: %+v", res)
	}
	if _, err := e.svc.SetRetention(ctx, Retention{ClosedConversationDays: ptr(365), AttachmentDays: ptr(90), RawMailDays: ptr(30)}, e.anna); err != nil {
		t.Fatal(err)
	}
	c, _ := e.svc.GetConversation(ctx, keep.ConversationID)
	_ = e.svc.UpdateConversation(ctx, c, mb, e.anna, UpdateInput{Status: ptr("closed")})
	later := time.Now().Add(100 * 24 * time.Hour)
	res, err := e.svc.ApplyRetention(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	if res.Attachments != 1 || res.RawMessages != 2 || res.ClosedConversations != 0 {
		t.Fatalf("after 100 days %+v", res)
	}
	res, _ = e.svc.ApplyRetention(ctx, time.Now().Add(400*24*time.Hour))
	if res.ClosedConversations != 1 {
		t.Fatalf("after 400 days %+v", res)
	}
	if _, err := e.svc.GetConversation(ctx, old.ConversationID); err != nil {
		t.Fatal("an open conversation is kept")
	}
}

func TestCustomerProfilesAndMerge(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	mb := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	a := e.deliverAndFetch(t, mb, mailFrom("Alice <alice@work.example>", mb.Email, "From work", ""))
	b := e.deliverAndFetch(t, mb, mailFrom("Alice Private <alice@home.example>", mb.Email, "From home", ""))
	ca, _ := e.svc.GetConversation(ctx, a.ConversationID)
	cb, _ := e.svc.GetConversation(ctx, b.ConversationID)
	if ca.Customer.ID == cb.Customer.ID {
		t.Fatal("two addresses are two customers until merged")
	}
	admin := e.admin()
	if err := e.svc.UpdateCustomer(ctx, admin, ca.Customer.ID, CustomerInput{AddEmail: ptr("alice@home.example")}, e.anna); err == nil {
		t.Fatal("an address of another customer needs a merge")
	}
	if err := e.svc.UpdateCustomer(ctx, admin, cb.Customer.ID, CustomerInput{Notes: ptr("VIP")}, e.anna); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.MergeCustomers(ctx, admin, ca.Customer.ID, cb.Customer.ID, e.anna); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.CustomerProfile(ctx, admin, ca.Customer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Emails) != 2 || len(p.Conversations) != 2 || p.Notes != "VIP" {
		t.Fatalf("profile %+v", p)
	}
	// [#n] threading accepts every address of the customer.
	res := e.deliverAndFetch(t, mb, mailFrom("alice@home.example", mb.Email, "Re: From work [#"+strconv.Itoa(ca.Number)+"]", ""))
	if res.ConversationID != ca.ID {
		t.Fatal("second address should thread by subject number")
	}
	outsider := NewAccess(uuid.New(), []string{"desk-press-viewer"}, nil)
	if _, err := e.svc.CustomerProfile(ctx, outsider, ca.Customer.ID); err == nil {
		t.Fatal("outsiders get 404")
	}
}

func TestNotifications(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	mb := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	bob := agent(t, e.svc.pool, "Bob")
	_, _ = e.svc.pool.Exec(ctx, `UPDATE users SET groups = '{desk-info-manager}' WHERE id = $1`, bob.ID)
	carl := agent(t, e.svc.pool, "Carl")
	_, _ = e.svc.pool.Exec(ctx, `UPDATE users SET groups = '{desk-press-viewer}' WHERE id = $1`, carl.ID)
	for _, u := range []*Person{bob, carl} {
		if _, err := e.svc.SetNotificationPrefs(ctx, u.ID, NotificationPrefs{NewConversation: true, AssignedToMe: true}); err != nil {
			t.Fatal(err)
		}
	}
	res := e.deliverAndFetch(t, mb, mailFrom("alice@example.com", mb.Email, "Wheelchair access", ""))
	outbox := func(to string) []string {
		rows, _ := e.svc.pool.Query(ctx, `SELECT subject FROM outbox WHERE to_email = $1 ORDER BY created_at`, to)
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			out = append(out, s)
		}
		return out
	}
	if got := outbox(bob.Email); len(got) != 1 || !strings.Contains(got[0], "New: Wheelchair access") {
		t.Fatalf("bob %v", got)
	}
	if got := outbox(carl.Email); len(got) != 0 {
		t.Fatalf("carl cannot see info: %v", got)
	}
	c, _ := e.svc.GetConversation(ctx, res.ConversationID)
	if err := e.svc.UpdateConversation(ctx, c, mb, e.anna, UpdateInput{AssigneeID: &bob.ID}); err != nil {
		t.Fatal(err)
	}
	if got := outbox(bob.Email); len(got) != 2 || !strings.Contains(got[1], "Assigned to you") {
		t.Fatalf("bob %v", got)
	}
	prefs, _ := e.svc.GetNotificationPrefs(ctx, bob.ID)
	if !prefs.Available || !prefs.NewConversation {
		t.Fatalf("%+v", prefs)
	}
}

func TestViewerPrune(t *testing.T) {
	e := newGoogleEnv(t)
	ctx := context.Background()
	mb := e.mailbox(t, ConnectorGoogleUser, "info@example.org")
	res := e.deliverAndFetch(t, mb, mailFrom("alice@example.com", mb.Email, "x", ""))
	_ = e.svc.Heartbeat(ctx, res.ConversationID, e.anna.ID, "viewing")
	_, _ = e.svc.pool.Exec(ctx, `UPDATE viewers SET seen_at = now() - interval '10 minutes'`)
	e.svc.pruneViewers(ctx)
	var n int
	_ = e.svc.pool.QueryRow(ctx, `SELECT count(*) FROM viewers`).Scan(&n)
	if n != 0 {
		t.Fatal("stale heartbeat kept")
	}
}
