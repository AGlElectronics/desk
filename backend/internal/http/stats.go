package http

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

type statsMetric struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// internalStatsHandler serves GET /api/internal/stats for the Stats module.
// Authenticated by STATS_PULL_TOKEN; without a token the route 404s.
func internalStatsHandler(token string, pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token = strings.TrimSpace(token)
		if token == "" {
			http.NotFound(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) != 1 {
			httperror.Render(w, r, httperror.New(httperror.CodeUnauthorized, "invalid service token"))
			return
		}
		metrics := collectStats(r.Context(), pool)
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": 1,
			"module":         "desk",
			"generated_at":   time.Now().UTC(),
			"metrics":        metrics,
		})
	}
}

func collectStats(ctx context.Context, pool *pgxpool.Pool) []statsMetric {
	metrics := make([]statsMetric, 0, 2)
	var v0 float64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::float8 FROM conversations WHERE status IS DISTINCT FROM 'closed' AND status IS DISTINCT FROM 'trashed'`).Scan(&v0); err != nil {
		v0 = 0
	}
	metrics = append(metrics, statsMetric{Key: "open_conversations", Label: "Open conversations", Value: v0, Unit: "count"})
	var v1 float64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::float8 FROM mailboxes`).Scan(&v1); err != nil {
		v1 = 0
	}
	metrics = append(metrics, statsMetric{Key: "mailboxes", Label: "Mailboxes", Value: v1, Unit: "count"})
	return metrics
}
