// Package http wires the chi router and ties handlers to their
// dependencies. The structure deliberately mirrors the other Con Services
// modules: one Deps struct consumed by one NewRouter, with per-resource
// handler types owning their own routing closure.
//
// API shape:
//
//	GET /livez             — process liveness (unauthenticated)
//	GET /readyz            — service readiness (unauthenticated)
//	GET /api/health        — richer status for humans
//	GET /api/den/state     — Den mode and pairing (unauthenticated; also /api/v1/den/state)
//	GET /api/role-catalog  — public role catalog for HR's access picker
//	GET /api/v1/brand      — active brand package (unauthenticated)
//	GET /api/v1/me         — authenticated principal + permissions
//	GET|POST /api/v1/mailboxes, GET|PATCH|DELETE /api/v1/mailboxes/{mailbox}
//	POST /api/v1/mailboxes/{mailbox}/{test|fetch}
//	GET|POST /api/v1/conversations (?mailbox=&folder=&q=)
//	GET|PATCH /api/v1/conversations/{conversation}
//	POST /api/v1/conversations/{conversation}/{threads|viewing}
//	POST /api/v1/threads/{thread}/{undo|retry}
//	PATCH /api/v1/customers/{customer}
//	GET /api/v1/attachments/{attachment}
//	GET /api/v1/users      — assignee picker
//	/api/v1/den…           — Den pairing admin (settings.manage)
//
// Everything under /api/v1 (except /brand) requires authentication; the
// auth middleware is applied inside a chi.Group so the shape is explicit.
// With DEN_MODE=required, the /api routes other than health, role catalog,
// brand, me and den answer 503 den_pairing_required until Den pairs.
package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
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
		api.Get("/role-catalog", roleCatalogHandler(deps.Access))
		api.Get("/den/state", den.StateHandler)

		api.Route("/v1", func(v1 chi.Router) {
			v1.Get("/brand", brandH.get)
			v1.Get("/brand/{role}", den.AssetHandler)
			v1.Get("/den/state", den.StateHandler)

			v1.Group(func(secure chi.Router) {
				secure.Use(authMW)
				secure.Get("/me", meHandler)
				prefs := den.PreferencesHandler(func(r *http.Request) (string, string, bool) {
					u, ok := auth.FromContext(r.Context())
					if !ok || u.Issuer == "" || u.Subject == "" {
						return "", "", false
					}
					return u.Issuer, u.Subject, true
				})
				secure.Get("/me/preferences", prefs)
				secure.Put("/me/preferences", prefs)
				secure.Route("/den", func(dr chi.Router) {
					dr.Use(requirePermission(domain.PermSettingsManage))
					dr.Get("/", den.StatusHandler)
					dr.Post("/pair", den.PairHandler)
					dr.Post("/sync", den.SyncHandler)
					dr.Delete("/pair", den.UnpairHandler)
				})

				secure.Group(func(work chi.Router) {
					work.Use(requirePermission(domain.PermTicketsWork))
					work.Get("/users", dh.listPeople)
					work.Get("/mailboxes", dh.listMailboxes)
					work.Get("/conversations", dh.listConversations)
					work.Post("/conversations", dh.createConversation)
					work.Get("/conversations/{conversation}", dh.getConversation)
					work.Patch("/conversations/{conversation}", dh.updateConversation)
					work.Post("/conversations/{conversation}/threads", dh.addThread)
					work.Post("/conversations/{conversation}/viewing", dh.heartbeat)
					work.Post("/threads/{thread}/undo", dh.threadAction(func(s *desk.Service, r *http.Request, id uuid.UUID) error {
						return s.UndoReply(r.Context(), id)
					}))
					work.Post("/threads/{thread}/retry", dh.threadAction(func(s *desk.Service, r *http.Request, id uuid.UUID) error {
						return s.RetryReply(r.Context(), id)
					}))
					work.Patch("/customers/{customer}", dh.updateCustomer)
					work.Get("/attachments/{attachment}", dh.downloadAttachment)
				})

				secure.Group(func(manage chi.Router) {
					manage.Use(requirePermission(domain.PermMailboxManage))
					manage.Post("/mailboxes", dh.createMailbox)
					manage.Get("/mailboxes/{mailbox}", dh.getMailbox)
					manage.Patch("/mailboxes/{mailbox}", dh.updateMailbox)
					manage.Delete("/mailboxes/{mailbox}", dh.deleteMailbox)
					manage.Post("/mailboxes/{mailbox}/test", dh.testMailbox)
					manage.Post("/mailboxes/{mailbox}/fetch", dh.fetchMailbox)
				})
			})
		})
	})

	return r
}
