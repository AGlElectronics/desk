// Package brand supplies the frontend brand package (product name, logo,
// colors, login copy). Standalone it comes from an optional local file;
// once Desk is paired with Den (internal/denlink), the HTTP layer overlays
// Den's organisation name, logo and theme on top.
package brand

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/AGlElectronics/desk/backend/internal/config"
)

// Brand is the public JSON shape returned by GET /api/v1/brand and consumed
// by the SPA on boot. Keep the fields small and additive — the frontend
// tolerates missing optional fields.
type Brand struct {
	Revision         int               `json:"revision"`
	Source           string            `json:"source"` // "standalone" | "den"
	ProductName      string            `json:"productName"`
	OrganizationName string            `json:"organizationName,omitempty"`
	LoginSubtitle    string            `json:"loginSubtitle,omitempty"`
	LoginButtonLabel string            `json:"loginButtonLabel,omitempty"`
	LogoURL          string            `json:"logoUrl,omitempty"`
	ThemeColor       string            `json:"themeColor,omitempty"`
	Colors           map[string]string `json:"colors,omitempty"`
	CSSVariables     map[string]string `json:"cssVariables,omitempty"`
	// Theme is Den's theme JSON (hex colours) when paired.
	Theme map[string]any `json:"theme,omitempty"`
	// Login screen background from Den: picture, "panel" | "centered", caption.
	LoginBackgroundURL string `json:"loginBackgroundUrl,omitempty"`
	LoginLayout        string `json:"loginLayout,omitempty"`
	LoginCaption       string `json:"loginCaption,omitempty"`
}

// Provider loads the local brand package. Implementations MUST be safe for
// concurrent reads and SHOULD cache — Get is called on every SPA boot.
type Provider interface {
	Get(ctx context.Context) (*Brand, error)
}

// New returns the standalone provider (local file or generic defaults).
func New(cfg *config.Config) Provider {
	return &standaloneProvider{path: cfg.BrandLocalPath}
}

// defaultBrand is returned when no local file is configured. It is
// intentionally generic — nothing hard-codes a specific convention here.
func defaultBrand() *Brand {
	return &Brand{
		Revision:         1,
		Source:           "standalone",
		ProductName:      "Desk",
		LoginSubtitle:    "Support inboxes & tickets",
		LoginButtonLabel: "Continue with your organisation login",
		ThemeColor:       "#18181b",
		Colors: map[string]string{
			"primary": "#4f46e5",
			"carbon":  "#18181b",
			"shadow":  "#241e20",
		},
	}
}

type standaloneProvider struct {
	path string

	mu     sync.RWMutex
	cached *Brand
	loaded time.Time
}

func (p *standaloneProvider) Get(_ context.Context) (*Brand, error) {
	// The file is read at most every 30 seconds.
	p.mu.RLock()
	if p.cached != nil && time.Since(p.loaded) < 30*time.Second {
		b := *p.cached
		p.mu.RUnlock()
		return &b, nil
	}
	p.mu.RUnlock()

	b := defaultBrand()
	if p.path != "" {
		data, err := os.ReadFile(p.path)
		if err != nil {
			return nil, fmt.Errorf("read brand file %q: %w", p.path, err)
		}
		if err := json.Unmarshal(data, b); err != nil {
			return nil, fmt.Errorf("parse brand file %q: %w", p.path, err)
		}
		b.Source = "standalone"
	}

	p.mu.Lock()
	p.cached = b
	p.loaded = time.Now()
	p.mu.Unlock()
	cp := *b
	return &cp, nil
}
