package desk

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/netguard"
)

const (
	dialTimeout    = 15 * time.Second
	sessionTimeout = 2 * time.Minute
	// maxPerFetch keeps one run short; the rest comes on the next run.
	maxPerFetch = 100
	// sizeBatch is how many UIDs one RFC822.SIZE request covers.
	sizeBatch = 25
	// maxBackoff caps the wait between attempts for a failing mailbox.
	maxBackoff = time.Hour
)

// errLogin marks an authentication failure (wrong username or password).
var errLogin = errors.New("login failed: check the username and password")

// RunFetcher polls every due mailbox every interval (and one mailbox right
// away when RequestFetch is called) until ctx is done. It runs in one
// goroutine, so a mailbox is never fetched twice at once.
func (s *Service) RunFetcher(ctx context.Context, interval time.Duration, logger zerolog.Logger) {
	log := logger.With().Str("component", "fetcher").Logger()
	s.fetchInterval = interval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.fetchAll(ctx, log)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.fetchAll(ctx, log)
		case id := <-s.fetchNow:
			mb, err := s.GetMailbox(ctx, id)
			if err == nil {
				s.fetchOne(ctx, mb, log)
			}
		}
	}
}

func (s *Service) fetchAll(ctx context.Context, log zerolog.Logger) {
	rows, err := s.pool.Query(ctx, `SELECT `+mailboxCols+` FROM mailboxes
		 WHERE imap_host <> '' AND (next_fetch_at IS NULL OR next_fetch_at <= now()) ORDER BY name`)
	if err != nil {
		if ctx.Err() == nil {
			log.Error().Err(err).Msg("list mailboxes")
		}
		return
	}
	var boxes []Mailbox
	for rows.Next() {
		mb, err := scanMailbox(rows)
		if err != nil {
			break
		}
		boxes = append(boxes, mb)
	}
	rows.Close()
	for _, mb := range boxes {
		if ctx.Err() != nil {
			return
		}
		s.fetchOne(ctx, mb, log)
	}
}

func (s *Service) fetchOne(ctx context.Context, mb Mailbox, log zerolog.Logger) {
	if mb.IMAPHost == "" {
		return
	}
	imported, err := s.fetchMailbox(ctx, mb, log)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		log.Warn().Err(err).Str("mailbox", mb.Email).Int("failures", mb.FetchFailures+1).Msg("fetch failed")
		wait := backoff(s.fetchInterval, mb.FetchFailures+1)
		_, _ = s.pool.Exec(ctx, `
			UPDATE mailboxes SET last_fetch_at = now(), last_fetch_error = $2,
			       fetch_failures = fetch_failures + 1, next_fetch_at = now() + $3::interval
			 WHERE id = $1`, mb.ID, describeMailError(err), fmt.Sprintf("%d seconds", int(wait.Seconds())))
		return
	}
	if imported > 0 {
		log.Info().Str("mailbox", mb.Email).Int("imported", imported).Msg("fetched mail")
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE mailboxes SET last_fetch_at = now(), last_fetch_error = '', fetch_failures = 0, next_fetch_at = NULL
		 WHERE id = $1`, mb.ID)
}

// backoff is the wait after the n-th failure in a row: the interval,
// doubled per failure, at most maxBackoff.
func backoff(interval time.Duration, failures int) time.Duration {
	if interval <= 0 {
		interval = time.Minute
	}
	wait := interval
	for i := 1; i < failures && wait < maxBackoff; i++ {
		wait *= 2
	}
	return min(wait, maxBackoff)
}

// describeMailError is what admins see as last_fetch_error and in
// "Test connection": the kind of failure, never raw server output.
func describeMailError(err error) string {
	var de displayError
	switch {
	case errors.As(err, &de):
		return de.msg
	case errors.Is(err, errLogin):
		return errLogin.Error()
	}
	return netguard.Describe(err)
}

// displayError carries a message that is already safe to show.
type displayError struct {
	msg string
	err error
}

func (e displayError) Error() string {
	if e.err != nil {
		return e.msg + ": " + e.err.Error()
	}
	return e.msg
}
func (e displayError) Unwrap() error { return e.err }

// dialIMAP connects and logs in.
func (s *Service) dialIMAP(ctx context.Context, mb Mailbox) (*imapclient.Client, error) {
	if mb.IMAPSecurity == "none" && !s.dev {
		return nil, displayError{msg: "plaintext IMAP is only allowed in development; choose TLS or STARTTLS"}
	}
	password, err := s.decrypt(mb.ID, "imap_password", mb.imapPasswordEnc)
	if err != nil {
		return nil, displayError{msg: err.Error()}
	}
	port := strconv.Itoa(mb.IMAPPort)
	tlsCfg := &tls.Config{ServerName: mb.IMAPHost, MinVersion: tls.VersionTLS12}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	var c *imapclient.Client
	switch mb.IMAPSecurity {
	case "tls":
		conn, err := s.guard.DialTLS(dctx, mb.IMAPHost, port, dialTimeout, tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		_ = conn.SetDeadline(time.Now().Add(sessionTimeout))
		c = imapclient.New(conn, nil)
	case "starttls":
		conn, err := s.guard.Dial(dctx, mb.IMAPHost, port, dialTimeout)
		if err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		_ = conn.SetDeadline(time.Now().Add(sessionTimeout))
		c, err = imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: tlsCfg})
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	default:
		conn, err := s.guard.Dial(dctx, mb.IMAPHost, port, dialTimeout)
		if err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		_ = conn.SetDeadline(time.Now().Add(sessionTimeout))
		c = imapclient.New(conn, nil)
	}
	if err := c.Login(mb.IMAPUsername, password).Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("%w (%v)", errLogin, err)
	}
	return c, nil
}

// fetchMailbox imports messages above the stored UID, at most maxPerFetch
// per run, oldest first. Each handled message is marked \Seen (and moved
// to the mailbox's "move to" folder when one is set); one that cannot be
// imported is recorded in import_failures and skipped.
func (s *Service) fetchMailbox(ctx context.Context, mb Mailbox, log zerolog.Logger) (int, error) {
	c, err := s.dialIMAP(ctx, mb)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	defer func() { _ = c.Logout().Wait() }()

	sel, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		return 0, displayError{msg: "the server has no INBOX folder", err: err}
	}
	lastUID := mb.lastUID
	if sel.UIDValidity != mb.uidValidity {
		// New or rebuilt mailbox: read it all again; the Message-ID index
		// drops what was already imported.
		lastUID = 0
		if _, err := s.pool.Exec(ctx, `UPDATE mailboxes SET uid_validity = $2, last_uid = 0 WHERE id = $1`,
			mb.ID, int64(sel.UIDValidity)); err != nil {
			return 0, err
		}
	}
	if sel.NumMessages == 0 {
		return 0, nil
	}

	var above imap.UIDSet
	above.AddRange(imap.UID(lastUID+1), 0) // 0 = "*"
	found, err := c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{above}}, nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("search: %w", err)
	}
	var uids []imap.UID
	for _, u := range found.AllUIDs() {
		// "N:*" always matches the last message, even below N.
		if uint32(u) > lastUID {
			uids = append(uids, u)
		}
	}
	slices.Sort(uids)
	if len(uids) > maxPerFetch {
		uids = uids[:maxPerFetch]
	}

	imported := 0
	for start := 0; start < len(uids); start += sizeBatch {
		batch := uids[start:min(start+sizeBatch, len(uids))]
		sizes, err := fetchSizes(c, batch)
		if err != nil {
			return imported, err
		}
		for _, uid := range batch {
			if ctx.Err() != nil {
				return imported, ctx.Err()
			}
			source := fmt.Sprintf("imap:%d:%d", sel.UIDValidity, uid)
			var raw []byte
			if size := sizes[uid]; size > maxMessageBytes {
				s.recordImportFailure(ctx, mb.ID, source, nil, size, fmt.Sprintf("message is larger than %d MB", maxMessageBytes>>20))
			} else if raw, err = fetchBody(c, uid); err != nil {
				return imported, err
			}
			if raw != nil {
				ok, err := s.ingest(ctx, mb, raw, source, log)
				if err != nil {
					return imported, err
				}
				if ok {
					imported++
				}
			}
			if err := s.markHandledIMAP(c, mb, uid); err != nil {
				log.Warn().Err(err).Str("mailbox", mb.Email).Uint32("uid", uint32(uid)).Msg("could not flag or move handled message")
			}
			if _, err := s.pool.Exec(ctx, `UPDATE mailboxes SET last_uid = $2 WHERE id = $1`, mb.ID, int64(uid)); err != nil {
				return imported, err
			}
		}
	}
	return imported, nil
}

func fetchSizes(c *imapclient.Client, uids []imap.UID) (map[imap.UID]int64, error) {
	set := imap.UIDSetNum(uids...)
	msgs, err := c.Fetch(set, &imap.FetchOptions{UID: true, RFC822Size: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch sizes: %w", err)
	}
	out := make(map[imap.UID]int64, len(msgs))
	for _, m := range msgs {
		out[m.UID] = m.RFC822Size
	}
	return out, nil
}

func fetchBody(c *imapclient.Client, uid imap.UID) ([]byte, error) {
	section := &imap.FetchItemBodySection{Peek: true}
	msgs, err := c.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch uid %d: %w", uid, err)
	}
	for _, m := range msgs {
		if m.UID == uid {
			if raw := m.FindBodySection(section); raw != nil {
				return raw, nil
			}
		}
	}
	return []byte{}, nil
}

func (s *Service) markHandledIMAP(c *imapclient.Client, mb Mailbox, uid imap.UID) error {
	set := imap.UIDSetNum(uid)
	if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
		return err
	}
	if mb.IMAPMoveTo != "" {
		if _, err := c.Move(set, mb.IMAPMoveTo).Wait(); err != nil {
			return fmt.Errorf("move to %q: %w", mb.IMAPMoveTo, err)
		}
	}
	return nil
}

// ingest parses and imports one raw message. It returns ok=true when a
// thread was added. A message Desk cannot store is recorded as an import
// failure and skipped (nil error); only infrastructure errors (database
// unreachable, cancelled) are returned, and then the run stops without
// moving past the message.
func (s *Service) ingest(ctx context.Context, mb Mailbox, raw []byte, source string, log zerolog.Logger) (bool, error) {
	if len(raw) > maxMessageBytes {
		s.recordImportFailure(ctx, mb.ID, source, nil, int64(len(raw)), fmt.Sprintf("message is larger than %d MB", maxMessageBytes>>20))
		return false, nil
	}
	p, err := ParseMessage(raw)
	if err != nil {
		log.Warn().Err(err).Str("mailbox", mb.Email).Str("source", source).Msg("unparseable message skipped")
		s.recordImportFailure(ctx, mb.ID, source, nil, int64(len(raw)), "the message could not be parsed")
		return false, nil
	}
	res, err := s.Import(ctx, mb, p, raw)
	if err != nil {
		// The server rejected this message's data: skip it. Anything else
		// (connection, S3, cancellation) is retried on the next run.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			log.Warn().Err(err).Str("mailbox", mb.Email).Str("source", source).Msg("message could not be stored; skipped")
			s.recordImportFailure(ctx, mb.ID, source, p, int64(len(raw)), "the message could not be stored")
			return false, nil
		}
		return false, err
	}
	return !res.Duplicate && res.Skipped == "", nil
}

func (s *Service) recordImportFailure(ctx context.Context, mailboxID uuid.UUID, source string, p *ParsedMessage, size int64, reason string) {
	var subject, from string
	if p != nil {
		subject, from = truncate(p.Subject, 300), p.FromEmail
	}
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO import_failures (mailbox_id, source, subject, from_email, size_bytes, error)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (mailbox_id, source) DO UPDATE SET error = EXCLUDED.error, created_at = now()`,
		mailboxID, source, subject, from, size, reason)
}

// ImportFailure is one message Desk skipped.
type ImportFailure struct {
	ID        uuid.UUID `json:"id"`
	Source    string    `json:"source"`
	Subject   string    `json:"subject"`
	FromEmail string    `json:"from_email"`
	SizeBytes int64     `json:"size_bytes"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}

// ImportFailures lists the newest skipped messages of a mailbox.
func (s *Service) ImportFailures(ctx context.Context, mailboxID uuid.UUID) ([]ImportFailure, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, source, subject, from_email, size_bytes, error, created_at
		  FROM import_failures WHERE mailbox_id = $1 ORDER BY created_at DESC LIMIT 100`, mailboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportFailure{}
	for rows.Next() {
		var f ImportFailure
		if err := rows.Scan(&f.ID, &f.Source, &f.Subject, &f.FromEmail, &f.SizeBytes, &f.Error, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ConnectionTest is the result of "Test connection" on a mailbox.
type ConnectionTest struct {
	IMAPOK    bool   `json:"imap_ok"`
	IMAPError string `json:"imap_error,omitempty"`
	SMTPOK    bool   `json:"smtp_ok"`
	SMTPError string `json:"smtp_error,omitempty"`
	SMTPVia   string `json:"smtp_via"`
}

// TestConnection logs in over IMAP and SMTP without changing anything.
// Errors are described in general terms (see describeMailError), so the
// test cannot be used to probe other hosts and ports.
func (s *Service) TestConnection(ctx context.Context, id uuid.UUID, log zerolog.Logger) (ConnectionTest, error) {
	mb, err := s.GetMailbox(ctx, id)
	if err != nil {
		return ConnectionTest{}, err
	}
	var out ConnectionTest
	if mb.IMAPHost == "" {
		out.IMAPError = "no IMAP server configured"
	} else if c, err := s.dialIMAP(ctx, mb); err != nil {
		log.Info().Err(err).Str("mailbox", mb.Email).Msg("imap connection test failed")
		out.IMAPError = describeMailError(err)
	} else {
		if _, err := c.Select("INBOX", nil).Wait(); err != nil {
			out.IMAPError = "the server has no INBOX folder"
		} else {
			out.IMAPOK = true
		}
		_ = c.Logout().Wait()
		c.Close()
	}
	cfg, err := s.smtpFor(mb)
	if err != nil {
		out.SMTPError = describeMailError(err)
		return out, nil
	}
	out.SMTPVia = cfg.label()
	if cl, err := s.dialSMTP(ctx, cfg); err != nil {
		log.Info().Err(err).Str("mailbox", mb.Email).Msg("smtp connection test failed")
		out.SMTPError = describeMailError(err)
	} else {
		out.SMTPOK = true
		_ = cl.Quit()
	}
	return out, nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
