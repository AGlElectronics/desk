// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ModulesCacheTTL is how long Modules reuses Den's paired-module list in
// memory. The list is never written to the database. A successful sync,
// pairing again, unpairing, or a rejected credential drops it at once,
// the same moments as the secrets cache.
const ModulesCacheTTL = time.Minute

// modulesResponseLimit caps GET /api/v1/integrations/modules. The list is
// a handful of addresses; anything larger is refused and not cached.
const modulesResponseLimit = 64 << 10

const modulesPath = "/api/v1/integrations/modules"

// Module is one enabled, paired module from Den's
// GET /api/v1/integrations/modules. RoleCatalogURL is the address of that
// module's GET /api/role-catalog (Den builds it from internal_url, or
// base_url when internal_url is empty). It is empty when the module
// publishes no catalog. HR's catalog discovery maps Slug and
// RoleCatalogURL onto its source list and skips an empty RoleCatalogURL;
// do not append /api/role-catalog again.
type Module struct {
	Slug           string `json:"slug"`
	DisplayName    string `json:"display_name"`
	BaseURL        string `json:"base_url"`
	InternalURL    string `json:"internal_url"`
	RoleCatalogURL string `json:"role_catalog_url"`
}

// Modules returns Den's enabled, paired modules for this organisation.
// The call uses the module's pairing credential
// (GET /api/v1/integrations/modules). ErrNotPaired means the module is
// standalone; ErrRevoked means Den rejected the credential. A successful
// answer is cached in memory for ModulesCacheTTL. A response larger than
// 64 KiB is an error and is not cached.
func (c *Client) Modules(ctx context.Context) ([]Module, error) {
	if c == nil || c.pool == nil {
		return nil, ErrNotPaired
	}
	l, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	if l == nil || l.credential == "" {
		c.clearSecretCache()
		return nil, ErrNotPaired
	}
	if l.revokedAt != nil {
		c.clearSecretCache()
		return nil, ErrRevoked
	}
	c.modulesMu.Lock()
	epoch := c.modulesEpoch
	if c.modulesOK && time.Since(c.modulesAt) < ModulesCacheTTL {
		out := append([]Module(nil), c.modules...)
		c.modulesMu.Unlock()
		return out, nil
	}
	c.modulesMu.Unlock()

	mods, err := c.fetchModules(ctx, l)
	if err != nil {
		return nil, err
	}
	c.modulesMu.Lock()
	defer c.modulesMu.Unlock()
	if c.modulesEpoch == epoch {
		c.modules = append([]Module(nil), mods...)
		c.modulesAt = time.Now()
		c.modulesOK = true
	}
	return append([]Module(nil), mods...), nil
}

func (c *Client) clearModulesCache() {
	if c == nil {
		return
	}
	c.modulesMu.Lock()
	c.modules = nil
	c.modulesAt = time.Time{}
	c.modulesOK = false
	c.modulesEpoch++
	c.modulesMu.Unlock()
}

func (c *Client) fetchModules(ctx context.Context, l *link) ([]Module, error) {
	res, err := c.get(ctx, l, modulesPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, modulesResponseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read den modules: %w", err)
	}
	if len(raw) > modulesResponseLimit {
		return nil, errors.New("den modules response exceeds 64 KiB")
	}
	return parseModules(raw)
}

func parseModules(raw []byte) ([]Module, error) {
	var probe any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, errors.New("unexpected modules from den")
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, errors.New("unexpected modules from den")
	}
	var body struct {
		Modules []Module `json:"modules"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("unexpected modules from den")
	}
	if body.Modules == nil {
		body.Modules = []Module{}
	}
	return body.Modules, nil
}
