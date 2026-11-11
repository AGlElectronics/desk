// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// SecretCacheTTL is how long ModuleSettingSecret reuses Den's answer in
// memory. The value is never written to the database. A successful sync,
// pairing again, unpairing, or a rejected credential drops it at once.
const SecretCacheTTL = time.Minute

// secretResponseLimit caps a secrets response. Den's document is a
// password or two; anything larger is refused and not cached.
const secretResponseLimit = 64 << 10

const secretsPath = "/api/v1/integrations/module-settings/secrets"

// ModuleSettingSecret returns one value from Den's
// GET /api/v1/integrations/module-settings/secrets. key is a dotted path
// such as "smtp.password", matching the JSON Den sends. An empty string
// and a nil error means Den holds no such value (the key is absent or
// null). ErrNotPaired means the module is standalone; ErrRevoked means Den
// rejected the credential. The answer is cached in memory for
// SecretCacheTTL. A response larger than 64 KiB is an error and is not
// cached.
func (c *Client) ModuleSettingSecret(ctx context.Context, key string) (string, error) {
	key, err := normalizeSecretKey(key)
	if err != nil {
		return "", err
	}
	if c == nil || c.pool == nil {
		return "", ErrNotPaired
	}
	l, err := c.load(ctx)
	if err != nil {
		return "", err
	}
	if l == nil || l.credential == "" {
		c.clearSecretCache()
		return "", ErrNotPaired
	}
	if l.revokedAt != nil {
		c.clearSecretCache()
		return "", ErrRevoked
	}
	raw, err := c.secretsDocument(ctx, l)
	if err != nil {
		return "", err
	}
	return secretAt(raw, key)
}

// SMTPPassword is ModuleSettingSecret(ctx, "smtp.password"). An empty
// string and a nil error means Den holds no SMTP password.
func (c *Client) SMTPPassword(ctx context.Context) (string, error) {
	return c.ModuleSettingSecret(ctx, "smtp.password")
}

func normalizeSecretKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("module setting secret key is empty")
	}
	for _, p := range strings.Split(key, ".") {
		if p == "" {
			return "", fmt.Errorf("module setting secret key %q is invalid", key)
		}
	}
	return key, nil
}

// clearSecretCache drops Den answers that are not the bundle: the secrets
// document and the paired-module list. A successful sync, pairing again,
// unpairing, or a rejected credential calls it.
func (c *Client) clearSecretCache() {
	if c == nil {
		return
	}
	c.secretsMu.Lock()
	c.secretRaw = nil
	c.secretAt = time.Time{}
	c.secretEpoch++
	c.secretsMu.Unlock()
	c.clearModulesCache()
}

// secretsDocument returns the cached secrets JSON, or fetches it. The
// cache is stored only when this call's epoch is still current, so an
// Unpair or a rejected credential during the request does not keep the
// password afterwards.
func (c *Client) secretsDocument(ctx context.Context, l *link) ([]byte, error) {
	c.secretsMu.Lock()
	epoch := c.secretEpoch
	if c.secretRaw != nil && time.Since(c.secretAt) < SecretCacheTTL {
		raw := append([]byte(nil), c.secretRaw...)
		c.secretsMu.Unlock()
		return raw, nil
	}
	c.secretsMu.Unlock()

	raw, err := c.fetchSecrets(ctx, l)
	if err != nil {
		return nil, err
	}
	c.secretsMu.Lock()
	defer c.secretsMu.Unlock()
	if c.secretEpoch == epoch {
		c.secretRaw = append([]byte(nil), raw...)
		c.secretAt = time.Now()
	}
	return raw, nil
}

func (c *Client) fetchSecrets(ctx context.Context, l *link) ([]byte, error) {
	res, err := c.get(ctx, l, secretsPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, secretResponseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read den secrets: %w", err)
	}
	if len(raw) > secretResponseLimit {
		return nil, errors.New("den secrets response exceeds 64 KiB")
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, errors.New("unexpected secrets from den")
	}
	return raw, nil
}

// secretAt walks a dotted key. A missing or null value is "", nil. A
// present value that is not a string is an error, so a wrong type is not
// mistaken for "Den holds none".
func secretAt(raw []byte, key string) (string, error) {
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return "", errors.New("unexpected secrets from den")
	}
	parts := strings.Split(key, ".")
	for i, p := range parts {
		obj, ok := node.(map[string]any)
		if !ok {
			return "", fmt.Errorf("den module setting secret %s is not a string", key)
		}
		v, exists := obj[p]
		if !exists || v == nil {
			return "", nil
		}
		if i == len(parts)-1 {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("den module setting secret %s is not a string", key)
			}
			return s, nil
		}
		node = v
	}
	return "", nil
}
