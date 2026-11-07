// Package denlink pairs this module with Den and keeps a local copy of Den's
// brand and organisation settings. It is identical in every Con Services
// module (HR, Stash, Rally, Desk); keep the copies in sync.
//
// With DEN_MODE=required (Config.Mode) the module refuses its API with 503
// den_pairing_required until it is paired; see RequirePaired.
//
// Flow: an admin mints a pairing URL in Den (Admin → Modules) and pastes it
// into this module. The backend redeems it for a long-lived module
// credential, stores it in the module's own database (table den_link), and
// pulls GET /api/v1/integrations/bundle every minute. Without a pairing the
// module is standalone and uses its own settings and default look.
package denlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// SyncInterval is how often a paired module pulls from Den.
const SyncInterval = time.Minute

// Brand is Den's brand bundle.
type Brand struct {
	Revision         int               `json:"revision"`
	UpdatedAt        string            `json:"updated_at,omitempty"`
	OrganizationName string            `json:"organization_name"`
	Theme            map[string]any    `json:"theme"`
	Typography       map[string]any    `json:"typography"`
	Copy             map[string]any    `json:"copy"`
	Assets           map[string]string `json:"assets"`
}

// Bundle is what Den returns from /api/v1/integrations/bundle.
type Bundle struct {
	Brand    Brand `json:"brand"`
	Settings struct {
		Data      map[string]any `json:"data"`
		UpdatedAt *string        `json:"updated_at"`
	} `json:"settings"`
}

// Status is shown on the module's settings page.
type Status struct {
	Paired     bool       `json:"paired"`
	DenBaseURL string     `json:"den_base_url,omitempty"`
	ModuleID   string     `json:"module_id,omitempty"`
	PairedAt   *time.Time `json:"paired_at,omitempty"`
	SyncedAt   *time.Time `json:"synced_at,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

// Mode is DEN_MODE: whether the module may serve its API before it is
// paired with Den.
type Mode string

const (
	// ModeStandalone serves the API whether or not the module is paired.
	ModeStandalone Mode = "standalone"
	// ModeRequired answers 503 den_pairing_required until the module is
	// paired (see RequirePaired).
	ModeRequired Mode = "required"
)

// Validate rejects anything but required and standalone. The empty Mode
// means standalone.
func (m Mode) Validate() error {
	switch m {
	case "", ModeStandalone, ModeRequired:
		return nil
	}
	return fmt.Errorf("DEN_MODE must be %q or %q, got %q", ModeRequired, ModeStandalone, string(m))
}

// Config wires a Client.
type Config struct {
	// Slug and Version are sent to Den when redeeming a pairing URL.
	Slug, Version string
	// InternalURL, when set, replaces the scheme and host of pasted pairing
	// URLs (e.g. http://den-backend:8080 inside Docker, where the URL Den
	// shows the admin is not reachable).
	InternalURL string
	// Mode is DEN_MODE; empty means standalone.
	Mode Mode
}

// Asset roles the module copies from Den and serves itself.
const (
	AssetLogo            = "logo"
	AssetLoginBackground = "login-background"
)

var syncedAssets = []string{AssetLogo, AssetLoginBackground}

type asset struct {
	id    string
	data  []byte
	ctype string
}

// Client is safe for concurrent use.
type Client struct {
	pool *pgxpool.Pool
	cfg  Config
	http *http.Client
	log  zerolog.Logger

	mu     sync.RWMutex
	bundle *Bundle
	assets map[string]asset
	// linked mirrors whether the den_link row exists.
	linked bool
}

// New returns a Client; call Run to start syncing.
func New(pool *pgxpool.Pool, cfg Config, log zerolog.Logger) *Client {
	return &Client{pool: pool, cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}, log: log, assets: map[string]asset{}}
}

// ErrNotPaired is returned when an operation needs a pairing.
var ErrNotPaired = errors.New("not connected to Den")

type link struct {
	baseURL, credential, moduleID string
	pairedAt                      time.Time
	syncedAt                      *time.Time
	lastError                     string
	bundle                        []byte
}

func (c *Client) load(ctx context.Context) (*link, error) {
	var l link
	err := c.pool.QueryRow(ctx, `
		SELECT den_base_url, credential, module_id, paired_at, synced_at, last_error, bundle
		  FROM den_link WHERE id = 1`).Scan(&l.baseURL, &l.credential, &l.moduleID, &l.pairedAt, &l.syncedAt, &l.lastError, &l.bundle)
	if errors.Is(err, pgx.ErrNoRows) {
		c.setLinked(false)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.setLinked(true)
	return &l, nil
}

func (c *Client) setLinked(v bool) {
	c.mu.Lock()
	c.linked = v
	c.mu.Unlock()
}

// Restore loads the pairing state and the cached bundle from the database.
// Call it before serving requests so RequirePaired sees the stored pairing;
// Run calls it too.
func (c *Client) Restore(ctx context.Context) error {
	l, err := c.load(ctx)
	if err != nil {
		return err
	}
	if l != nil && len(l.bundle) > 0 {
		var b Bundle
		if json.Unmarshal(l.bundle, &b) == nil {
			c.mu.Lock()
			c.bundle = &b
			c.mu.Unlock()
		}
	}
	return nil
}

// Run restores the cached bundle and syncs every SyncInterval until ctx ends.
func (c *Client) Run(ctx context.Context) {
	if err := c.Restore(ctx); err != nil {
		c.log.Warn().Err(err).Msg("den link: load failed")
	}
	t := time.NewTicker(SyncInterval)
	defer t.Stop()
	for {
		if err := c.Sync(ctx); err != nil && !errors.Is(err, ErrNotPaired) {
			c.log.Warn().Err(err).Msg("den link: sync failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Bundle is the last synced bundle, or nil when standalone.
func (c *Client) Bundle() *Bundle {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bundle
}

// Setting returns a string org setting from Den. ok is false when the module
// is standalone or Den does not set the key (then the module's own value applies).
func (c *Client) Setting(key string) (value string, ok bool) {
	b := c.Bundle()
	if b == nil {
		return "", false
	}
	v, present := b.Settings.Data[key]
	if !present {
		return "", false
	}
	s, isStr := v.(string)
	return s, isStr
}

// OrganizationName is Den's resolved organisation name ("" when standalone).
func (c *Client) OrganizationName() string {
	if b := c.Bundle(); b != nil {
		return b.Brand.OrganizationName
	}
	return ""
}

// Paired reports whether the module currently follows Den.
func (c *Client) Paired() bool { return c.Bundle() != nil }

// Linked reports whether the module holds a pairing (the den_link row
// exists), even before the first sync or after Den rejected the credential.
// It reads a cached flag kept current by Restore, Pair, Unpair and Sync.
func (c *Client) Linked() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.linked
}

// Status reports the pairing state.
func (c *Client) Status(ctx context.Context) (Status, error) {
	l, err := c.load(ctx)
	if err != nil || l == nil {
		return Status{}, err
	}
	pairedAt := l.pairedAt
	return Status{Paired: true, DenBaseURL: l.baseURL, ModuleID: l.moduleID, PairedAt: &pairedAt, SyncedAt: l.syncedAt, LastError: l.lastError}, nil
}

// Pair redeems a pairing URL from Den and syncs right away.
func (c *Client) Pair(ctx context.Context, pairingURL string) (Status, error) {
	u, err := url.Parse(strings.TrimSpace(pairingURL))
	if err != nil || u.Scheme == "" || u.Host == "" || u.Query().Get("token") == "" {
		return Status{}, errors.New("paste the full pairing URL from Den (Admin → Modules → Pair)")
	}
	if c.cfg.InternalURL != "" {
		if iu, err := url.Parse(c.cfg.InternalURL); err == nil {
			u.Scheme, u.Host = iu.Scheme, iu.Host
		}
	}
	body, _ := json.Marshal(map[string]string{"module_slug": c.cfg.Slug, "module_version": c.cfg.Version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return Status{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("could not reach Den at %s: %w", u.Host, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("Den refused the pairing URL: %s", denError(res))
	}
	var out struct {
		Credential string `json:"credential"`
		ModuleID   string `json:"module_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.Credential == "" {
		return Status{}, errors.New("unexpected response from Den")
	}
	base := u.Scheme + "://" + u.Host
	if _, err := c.pool.Exec(ctx, `
		INSERT INTO den_link (id, den_base_url, credential, module_id, paired_at)
		VALUES (1, $1, $2, $3, now())
		ON CONFLICT (id) DO UPDATE SET den_base_url = EXCLUDED.den_base_url, credential = EXCLUDED.credential,
		       module_id = EXCLUDED.module_id, paired_at = now(), synced_at = NULL, last_error = '', bundle = NULL`,
		base, out.Credential, out.ModuleID); err != nil {
		return Status{}, err
	}
	c.setLinked(true)
	if err := c.Sync(ctx); err != nil {
		c.log.Warn().Err(err).Msg("den link: first sync failed")
	}
	return c.Status(ctx)
}

// Unpair forgets the credential; the module goes back to standalone.
func (c *Client) Unpair(ctx context.Context) error {
	if _, err := c.pool.Exec(ctx, `DELETE FROM den_link WHERE id = 1`); err != nil {
		return err
	}
	c.mu.Lock()
	c.bundle, c.assets, c.linked = nil, map[string]asset{}, false
	c.mu.Unlock()
	return nil
}

// Sync pulls the bundle (and the logo and login background when they
// changed) from Den.
func (c *Client) Sync(ctx context.Context) error {
	l, err := c.load(ctx)
	if err != nil {
		return err
	}
	if l == nil {
		return ErrNotPaired
	}
	b, raw, err := c.fetchBundle(ctx, l)
	for _, role := range syncedAssets {
		if err == nil {
			err = c.syncAsset(ctx, l, role, b.Brand.Assets[role])
		}
	}
	if err != nil {
		_, _ = c.pool.Exec(ctx, `UPDATE den_link SET last_error = $1 WHERE id = 1`, err.Error())
		return err
	}
	if _, err := c.pool.Exec(ctx, `UPDATE den_link SET bundle = $1, synced_at = now(), last_error = '' WHERE id = 1`, raw); err != nil {
		return err
	}
	c.mu.Lock()
	c.bundle = b
	c.mu.Unlock()
	return nil
}

func (c *Client) get(ctx context.Context, l *link, path string) (*http.Response, error) {
	return c.send(ctx, l, http.MethodGet, path, nil)
}

func (c *Client) send(ctx context.Context, l *link, method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, l.baseURL+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+l.credential)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach Den: %w", err)
	}
	if res.StatusCode == http.StatusUnauthorized {
		_ = res.Body.Close()
		return nil, errors.New("Den rejected this module's credential (revoked?); pair again")
	}
	if res.StatusCode != http.StatusOK {
		defer func() { _ = res.Body.Close() }()
		return nil, fmt.Errorf("Den returned %s", denError(res))
	}
	return res, nil
}

func (c *Client) fetchBundle(ctx context.Context, l *link) (*Bundle, []byte, error) {
	res, err := c.get(ctx, l, "/api/v1/integrations/bundle")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	var b Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, nil, fmt.Errorf("unexpected bundle from Den: %w", err)
	}
	if b.Settings.Data == nil {
		b.Settings.Data = map[string]any{}
	}
	return &b, raw, nil
}

func (c *Client) syncAsset(ctx context.Context, l *link, role, id string) error {
	c.mu.RLock()
	_, have := c.assets[role]
	same := id == c.assets[role].id && (id == "" || have)
	c.mu.RUnlock()
	if same {
		return nil
	}
	if id == "" {
		c.mu.Lock()
		delete(c.assets, role)
		c.mu.Unlock()
		return nil
	}
	res, err := c.get(ctx, l, "/api/v1/integrations/branding/assets/"+url.PathEscape(id))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 10<<20))
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.assets[role] = asset{id: id, data: data, ctype: res.Header.Get("Content-Type")}
	c.mu.Unlock()
	return nil
}

// Preferences are a person's UI choices. Den stores them so every app shares
// them; Synced is false when the module is standalone or Den is unreachable,
// and then the frontend keeps its locally cached choice.
type Preferences struct {
	Theme string `json:"theme"`
	// UpdatedAt is null until the person first picks a theme anywhere.
	UpdatedAt *string `json:"updated_at"`
	Synced    bool    `json:"synced"`
}

// Preferences loads a person's preferences from Den.
func (c *Client) Preferences(ctx context.Context, issuer, subject string) (Preferences, error) {
	l, err := c.load(ctx)
	if err != nil || l == nil {
		return Preferences{Theme: "system"}, err
	}
	q := url.Values{"issuer": {issuer}, "subject": {subject}}
	res, err := c.get(ctx, l, "/api/v1/integrations/user-preferences?"+q.Encode())
	if err != nil {
		return Preferences{Theme: "system"}, err
	}
	defer func() { _ = res.Body.Close() }()
	p := Preferences{Theme: "system"}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&p); err != nil {
		return Preferences{Theme: "system"}, fmt.Errorf("unexpected preferences from Den: %w", err)
	}
	p.Synced = true
	return p, nil
}

// SetPreferences saves a person's theme in Den.
func (c *Client) SetPreferences(ctx context.Context, issuer, subject, theme string) (Preferences, error) {
	l, err := c.load(ctx)
	if err != nil {
		return Preferences{}, err
	}
	if l == nil {
		return Preferences{}, ErrNotPaired
	}
	res, err := c.send(ctx, l, http.MethodPut, "/api/v1/integrations/user-preferences",
		map[string]string{"issuer": issuer, "subject": subject, "theme": theme})
	if err != nil {
		return Preferences{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var p Preferences
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&p); err != nil {
		return Preferences{}, fmt.Errorf("unexpected preferences from Den: %w", err)
	}
	p.Synced = true
	return p, nil
}

// Identity returns the signed-in person's IdP issuer and subject; ok is false
// when the request has none (preferences then stay local to the browser).
type Identity func(r *http.Request) (issuer, subject string, ok bool)

// PreferencesHandler serves GET and PUT <api>/me/preferences.
func (c *Client) PreferencesHandler(identity Identity) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issuer, subject, ok := identity(r)
		if r.Method == http.MethodGet {
			p := Preferences{Theme: "system"}
			if ok {
				var err error
				if p, err = c.Preferences(r.Context(), issuer, subject); err != nil {
					c.log.Warn().Err(err).Msg("den link: load preferences failed")
				}
			}
			writeJSON(w, http.StatusOK, p)
			return
		}
		var in struct {
			Theme string `json:"theme"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
		switch in.Theme {
		case "system", "light", "dark":
		default:
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "theme must be system, light or dark")
			return
		}
		if !ok {
			writeJSON(w, http.StatusOK, Preferences{Theme: in.Theme})
			return
		}
		p, err := c.SetPreferences(r.Context(), issuer, subject, in.Theme)
		if err != nil {
			if !errors.Is(err, ErrNotPaired) {
				c.log.Warn().Err(err).Msg("den link: save preferences failed")
			}
			writeJSON(w, http.StatusOK, Preferences{Theme: in.Theme})
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func denError(res *http.Response) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return res.Status
}

// --- HTTP ---------------------------------------------------------------

// PublicBrand is the module's GET <api>/brand response. Frontends apply
// Theme as CSS variables; when standalone, Theme is empty and the module's
// own defaults apply.
type PublicBrand struct {
	Source             string         `json:"source"` // "den" | "standalone"
	Revision           int            `json:"revision"`
	OrganizationName   string         `json:"organization_name"`
	LogoURL            string         `json:"logo_url,omitempty"`
	LoginBackgroundURL string         `json:"login_background_url,omitempty"`
	Theme              map[string]any `json:"theme"`
	// Copy includes login_layout ("panel" | "centered") and login_caption.
	Copy map[string]any `json:"copy"`
}

// BrandHandler serves PublicBrand. assetBase is where AssetHandler is
// mounted: <assetBase>/logo and <assetBase>/login-background.
func (c *Client) BrandHandler(assetBase string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := PublicBrand{Source: "standalone", Theme: map[string]any{}, Copy: map[string]any{}}
		if b := c.Bundle(); b != nil {
			out.Source, out.Revision, out.OrganizationName = "den", b.Brand.Revision, b.Brand.OrganizationName
			if b.Brand.Theme != nil {
				out.Theme = b.Brand.Theme
			}
			if b.Brand.Copy != nil {
				out.Copy = b.Brand.Copy
			}
			out.LogoURL = c.AssetURL(assetBase, AssetLogo)
			out.LoginBackgroundURL = c.AssetURL(assetBase, AssetLoginBackground)
		}
		w.Header().Set("Cache-Control", "no-cache")
		writeJSON(w, http.StatusOK, out)
	}
}

// AssetURL is <assetBase>/<role> with a cache-busting version, or "" when
// Den has no such asset.
func (c *Client) AssetURL(assetBase, role string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	a, ok := c.assets[role]
	if !ok {
		return ""
	}
	return assetBase + "/" + role + "?v=" + url.QueryEscape(a.id)
}

// AssetHandler serves a copied Den asset (so browsers never talk to Den).
// Mount it at <assetBase>/{role}; role is read from the last path segment.
func (c *Client) AssetHandler(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	c.mu.RLock()
	a, ok := c.assets[role]
	c.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such brand asset")
		return
	}
	ctype := a.ctype
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(a.data)
}

// State is the public GET /api/den/state response.
type State struct {
	Mode   Mode `json:"mode"`
	Paired bool `json:"paired"`
}

// StateHandler serves State without authentication so a frontend can show
// a "waiting for Den" screen before anyone signs in.
func (c *Client) StateHandler(w http.ResponseWriter, r *http.Request) {
	mode := c.cfg.Mode
	if mode == "" {
		mode = ModeStandalone
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, State{Mode: mode, Paired: c.Linked()})
}

// pairingExempt are the API paths (and everything below them) that stay
// open while RequirePaired blocks: health, brand, sign-in and the Den
// connection routes an admin needs to pair. Modules serve either /api or
// /api/v1, and Rally its submission site under /api/public/v1.
var pairingExempt = []string{
	"/api/health",
	"/api/role-catalog",
	"/api/brand",
	"/api/me",
	"/api/den",
	"/api/v1/brand",
	"/api/v1/me",
	"/api/v1/den",
	"/api/public/v1/brand",
}

func isPairingExempt(path string) bool {
	for _, p := range pairingExempt {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// RequirePaired answers 503 den_pairing_required while Mode is required and
// the module is not paired, except for the paths in pairingExempt. Mount it
// first on the module's /api router.
func (c *Client) RequirePaired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.cfg.Mode != ModeRequired || c.Linked() || isPairingExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusServiceUnavailable, "den_pairing_required", "This module is waiting to be connected to Den.")
	})
}

// Routes for admins: GET status, POST pair, POST sync, DELETE unpair.
// Mount behind the module's admin check.
func (c *Client) StatusHandler(w http.ResponseWriter, r *http.Request) {
	s, err := c.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (c *Client) PairHandler(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PairingURL string `json:"pairing_url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	s, err := c.Pair(r.Context(), in.PairingURL)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "pairing_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (c *Client) SyncHandler(w http.ResponseWriter, r *http.Request) {
	if err := c.Sync(r.Context()); err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, ErrNotPaired) {
			code = http.StatusConflict
		}
		writeError(w, code, "sync_failed", err.Error())
		return
	}
	c.StatusHandler(w, r)
}

func (c *Client) UnpairHandler(w http.ResponseWriter, r *http.Request) {
	if err := c.Unpair(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
