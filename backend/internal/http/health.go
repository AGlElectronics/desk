package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// healthHandler serves the two orchestrator-facing probes:
//
//   - /livez checks only process liveness; it never pings the DB. A
//     restart on this endpoint should mean the process is deadlocked, not
//     that a dependency is temporarily down.
//   - /readyz checks the DB (and other critical deps) and returns 503 if
//     any are unavailable. Load balancers should route only to replicas
//     where /readyz is 200.
//
// GET /api/health keeps the Stash-shaped JSON for compatibility and simple
// human curling.
type healthHandler struct {
	pool      *pgxpool.Pool
	startedAt time.Time
	version   string
}

type healthResponse struct {
	Status    string `json:"status"`
	Database  string `json:"database"`
	Version   string `json:"version"`
	UptimeSec int64  `json:"uptime_sec"`
}

func newHealthHandler(pool *pgxpool.Pool, version string) *healthHandler {
	return &healthHandler{
		pool:      pool,
		startedAt: time.Now(),
		version:   version,
	}
}

// livez is intentionally trivial: 200 if the goroutine is scheduled.
func (h *healthHandler) livez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// readyz pings the DB with a short timeout. Returns 503 so k8s / nginx
// stops routing traffic while the DB is unreachable, but DOES NOT exit the
// process (/livez stays 200) so transient outages don't restart-loop.
func (h *healthHandler) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if h.pool == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"degraded","database":"not_configured"}`))
		return
	}
	if err := h.pool.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"degraded","database":"down"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","database":"ok"}`))
}

// get is the richer /api/health response used by the SPA and curl debugging.
func (h *healthHandler) get(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	dbStatus := "ok"
	if h.pool != nil {
		if err := h.pool.Ping(ctx); err != nil {
			dbStatus = "down"
		}
	} else {
		dbStatus = "not_configured"
	}

	overall := "ok"
	if dbStatus != "ok" {
		overall = "degraded"
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if overall != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(healthResponse{
		Status:    overall,
		Database:  dbStatus,
		Version:   h.version,
		UptimeSec: int64(time.Since(h.startedAt).Seconds()),
	})
}
