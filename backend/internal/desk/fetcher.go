package desk

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	dialTimeout    = 15 * time.Second
	sessionTimeout = 2 * time.Minute
	// maxPerFetch keeps one run short; the rest comes on the next tick.
	maxPerFetch = 100
)

// RunFetcher polls every mailbox with an IMAP host every interval (and one
// mailbox right away when RequestFetch is called) until ctx is done. It
// runs in one goroutine, so a mailbox is never fetched twice at once.
func (s *Service) RunFetcher(ctx context.Context, interval time.Duration, logger zerolog.Logger) {
	log := logger.With().Str("component", "fetcher").Logger()
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
	rows, err := s.pool.Query(ctx, `SELECT `+mailboxCols+` FROM mailboxes WHERE imap_host <> '' ORDER BY name`)
	if err != nil {
		log.Error().Err(err).Msg("list mailboxes")
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
	msg := ""
	if err != nil {
		msg = err.Error()
		log.Warn().Err(err).Str("mailbox", mb.Email).Msg("fetch failed")
	} else if imported > 0 {
		log.Info().Str("mailbox", mb.Email).Int("imported", imported).Msg("fetched mail")
	}
	_, _ = s.pool.Exec(ctx, `UPDATE mailboxes SET last_fetch_at = now(), last_fetch_error = $2 WHERE id = $1`, mb.ID, msg)
}

// dialIMAP connects and logs in.
func (s *Service) dialIMAP(mb Mailbox) (*imapclient.Client, error) {
	password, err := s.decrypt(mb.imapPasswordEnc)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(mb.IMAPHost, strconv.Itoa(mb.IMAPPort))
	tlsCfg := &tls.Config{ServerName: mb.IMAPHost, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: dialTimeout}
	var c *imapclient.Client
	switch mb.IMAPSecurity {
	case "tls":
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", addr, err)
		}
		_ = conn.SetDeadline(time.Now().Add(sessionTimeout))
		c = imapclient.New(conn, nil)
	case "starttls":
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", addr, err)
		}
		_ = conn.SetDeadline(time.Now().Add(sessionTimeout))
		c, err = imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: tlsCfg})
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	default:
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", addr, err)
		}
		_ = conn.SetDeadline(time.Now().Add(sessionTimeout))
		c = imapclient.New(conn, nil)
	}
	if err := c.Login(mb.IMAPUsername, password).Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("login as %s: %w", mb.IMAPUsername, err)
	}
	return c, nil
}

// fetchMailbox imports every message above the stored UID and marks the
// imported ones \Seen.
func (s *Service) fetchMailbox(ctx context.Context, mb Mailbox, log zerolog.Logger) (int, error) {
	c, err := s.dialIMAP(mb)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	defer func() { _ = c.Logout().Wait() }()

	sel, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("select INBOX: %w", err)
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

	var set imap.UIDSet
	set.AddRange(imap.UID(lastUID+1), 0) // 0 = "*"
	section := &imap.FetchItemBodySection{Peek: true}
	msgs, err := c.Fetch(set, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}

	imported := 0
	var seen imap.UIDSet
	for _, m := range msgs {
		// "N:*" always returns the last message, even below N.
		if uint32(m.UID) <= lastUID {
			continue
		}
		if imported >= maxPerFetch {
			break
		}
		raw := m.FindBodySection(section)
		if raw != nil {
			p, err := ParseMessage(raw)
			if err != nil {
				log.Warn().Err(err).Str("mailbox", mb.Email).Uint32("uid", uint32(m.UID)).Msg("unparseable message skipped")
			} else if res, err := s.Import(ctx, mb, p); err != nil {
				return imported, fmt.Errorf("import uid %d: %w", m.UID, err)
			} else if !res.Duplicate && res.Skipped == "" {
				imported++
			}
		}
		seen.AddNum(m.UID)
		if _, err := s.pool.Exec(ctx, `UPDATE mailboxes SET last_uid = $2 WHERE id = $1`, mb.ID, int64(m.UID)); err != nil {
			return imported, err
		}
	}
	if len(seen) > 0 {
		_ = c.Store(seen, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
	}
	return imported, nil
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
func (s *Service) TestConnection(ctx context.Context, id uuid.UUID) (ConnectionTest, error) {
	mb, err := s.GetMailbox(ctx, id)
	if err != nil {
		return ConnectionTest{}, err
	}
	var out ConnectionTest
	if mb.IMAPHost == "" {
		out.IMAPError = "no IMAP server configured"
	} else if c, err := s.dialIMAP(mb); err != nil {
		out.IMAPError = err.Error()
	} else {
		if _, err := c.Select("INBOX", nil).Wait(); err != nil {
			out.IMAPError = "select INBOX: " + err.Error()
		} else {
			out.IMAPOK = true
		}
		_ = c.Logout().Wait()
		c.Close()
	}
	cfg, err := s.smtpFor(mb)
	if err != nil {
		out.SMTPError = err.Error()
		return out, nil
	}
	out.SMTPVia = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	if cl, err := dialSMTP(cfg); err != nil {
		out.SMTPError = err.Error()
	} else {
		out.SMTPOK = true
		_ = cl.Quit()
	}
	return out, nil
}
