// Package http wires the chi router and ties handlers to their
// dependencies. The structure deliberately mirrors the other Con Services
// modules: one Deps struct consumed by one NewRouter, with per-resource
// handler types owning their own routing closure.
//
// API shape (docs: modules/desk/api):
//
//	GET /livez, /readyz, /api/health        — health (unauthenticated)
//	GET /api/den/state                      — Den mode and pairing (unauthenticated; also /api/v1/den/state)
//	GET /api/role-catalog                   — Desk admin plus three roles per mailbox, for HR
//	GET /api/v1/brand                       — active brand package (unauthenticated)
//	GET /api/v1/me                          — principal, permissions, mailbox rights
//	GET|PUT /api/v1/me/notifications        — email notification preferences
//	/api/v1/mailboxes…                      — list, create, view, change, trash, restore, purge, provision, test, fetch
//	/api/v1/conversations…                  — list/search, create, view, change, trash, threads, forward, merge, move, star
//	/api/v1/threads/{thread}…               — undo, retry, delete, restore
//	/api/v1/customers/{customer}…           — profile, change, merge
//	GET /api/v1/attachments/{attachment}
//	GET /api/v1/users, /api/v1/tags
//	/api/v1/saved-replies…
//	GET|PUT /api/v1/settings/retention, GET /api/v1/settings/integrations, GET /api/v1/audit (Desk admins)
//	/api/v1/den…                            — Den pairing admin (settings.manage)
//
// Everything under /api/v1 (except /brand and /den/state) requires
// authentication. With DEN_MODE=required, the /api routes other than
// health, role catalog, brand, me and den answer 503 den_pairing_required
// until Den pairs.
package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/brand"
	"github.com/AGlElectronics/desk/backend/internal/clientip"
	"github.com/AGlElectronics/desk/backend/internal/config"
	"github.com/AGlElectronics/desk/backend/internal/denlink"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	"github.com/AGlElectronics/desk/backend/internal/domain"
)

// Deps groups the runtime dependencies the HTTP layer needs.
type Deps struct {
	Config       *config.Config
	Pool         *pgxpool.Pool
	Logger       zerolog.Logger
	Brand        brand.Provider
	OIDCVerifier *auth.OIDCVerifier
	Access       *auth.AccessConfig
	// Den is the pairing with Den; nil means a fresh standalone client.
	Den  *denlink.Client
	Desk *desk.Service
}

// NewRouter builds the chi router with middleware, CORS, and routes.
// Version is passed in from cmd/server at build time.
func NewRouter(deps Deps, version string) http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(clientip.Middleware(deps.Config.TrustedProxyCIDRs))
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(60 * time.Second))

	r.Use(hlog.NewHandler(deps.Logger))
	r.Use(hlog.AccessHandler(func(req *http.Request, status, size int, duration time.Duration) {
		hlog.FromRequest(req).Info().
			Str("method", req.Method).
			Str("path", req.URL.Path).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Msg("http")
	}))
	r.Use(hlog.RequestIDHandler("request_id", "X-Request-ID"))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   deps.Config.CORSAllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Dev-User", "X-Dev-Groups", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID", "Content-Disposition"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	health := newHealthHandler(deps.Pool, version)
	resolver := auth.NewResolver(deps.Pool, deps.Access)
	authMW := auth.Middleware(deps.Config, resolver, deps.OIDCVerifier)

	den := deps.Den
	if den == nil {
		den = denlink.New(deps.Pool, denlink.Config{Slug: "desk", Version: version, Mode: deps.Config.DenMode}, deps.Logger)
	}
	brandH := &brandHandler{provider: deps.Brand, den: den}
	dh := &deskHandler{svc: deps.Desk}

	r.Get("/livez", health.livez)
	r.Get("/readyz", health.readyz)

	r.Route("/api", func(api chi.Router) {
		api.Use(den.RequirePaired)
		api.Get("/health", health.get)
		api.Get("/role-catalog", roleCatalogHandler(deps.Access, deps.Desk))
		api.Get("/den/state", den.StateHandler)

		api.Route("/v1", func(v1 chi.Router) {
			v1.Get("/brand", brandH.get)
			v1.Get("/brand/{role}", den.AssetHandler)
			v1.Get("/den/state", den.StateHandler)

			v1.Group(func(secure chi.Router) {
				secure.Use(authMW)
				secure.Get("/me", meHandler(deps.Desk))
				prefs := den.PreferencesHandler(func(r *http.Request) (string, string, bool) {
					u, ok := auth.FromContext(r.Context())
					if !ok || u.Issuer == "" || u.Subject == "" {
						return "", "", false
					}
					return u.Issuer, u.Subject, true
				})
				secure.Get("/me/preferences", prefs)
				secure.Put("/me/preferences", prefs)
				secure.Get("/me/notifications", handle(dh.getNotifications))
				secure.Put("/me/notifications", handle(dh.putNotifications))
				secure.Route("/den", func(dr chi.Router) {
					dr.Use(requirePermission(domain.PermSettingsManage))
					dr.Get("/", den.StatusHandler)
					dr.Post("/pair", den.PairHandler)
					dr.Post("/sync", den.SyncHandler)
					dr.Delete("/pair", den.UnpairHandler)
				})

				// Per-mailbox authorization happens in the handlers.
				secure.Route("/mailboxes", dh.mailboxRoutes)
				secure.Route("/conversations", dh.conversationRoutes)
				secure.Post("/threads/{thread}/undo", handle(dh.undoThread))
				secure.Post("/threads/{thread}/retry", handle(dh.retryThread))
				secure.Delete("/threads/{thread}", handle(dh.deleteThread))
				secure.Post("/threads/{thread}/restore", handle(dh.restoreThread))
				secure.Get("/customers/{customer}", handle(dh.getCustomer))
				secure.Patch("/customers/{customer}", handle(dh.updateCustomer))
				secure.Post("/customers/{customer}/merge", handle(dh.mergeCustomer))
				secure.Get("/attachments/{attachment}", dh.downloadAttachment)
				secure.Get("/users", handle(dh.listPeople))
				secure.Get("/tags", handle(dh.listTags))
				secure.Post("/saved-replies", handle(dh.createSavedReply))
				secure.Patch("/saved-replies/{reply}", handle(dh.updateSavedReply))
				secure.Delete("/saved-replies/{reply}", handle(dh.deleteSavedReply))
				secure.Get("/settings/retention", handle(dh.getRetention))
				secure.Put("/settings/retention", handle(dh.putRetention))
				secure.Get("/settings/integrations", handle(dh.getIntegrations))
				secure.Get("/audit", handle(dh.auditLog))
			})
		})
	})

	return r
}
