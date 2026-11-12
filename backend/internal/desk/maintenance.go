package desk

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// dailyEvery is how often the daily jobs run (purges, reminders, retention).
const dailyEvery = 24 * time.Hour

// RunMaintenance runs Desk's housekeeping until ctx is done: every minute
// it clears stale viewer heartbeats; once a day (remembered across
// restarts) it purges what retention and the trashes say is due and sends
// purge reminders.
func (s *Service) RunMaintenance(ctx context.Context, logger zerolog.Logger) {
	log := logger.With().Str("component", "maintenance").Logger()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		s.maintain(ctx, time.Now(), log)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) maintain(ctx context.Context, now time.Time, log zerolog.Logger) {
	s.pruneViewers(ctx)
	var last time.Time
	_ = s.getSetting(ctx, "maintenance_last_daily", &last)
	if now.Sub(last) < dailyEvery {
		return
	}
	s.RunDaily(ctx, now, log)
	_ = s.putSetting(ctx, "maintenance_last_daily", now, nil)
}

// RunDaily runs the daily jobs once.
func (s *Service) RunDaily(ctx context.Context, now time.Time, log zerolog.Logger) {
	s.purgeDueMailboxes(ctx, now, log)
	s.applyRetention(ctx, now, log)
}

// pruneViewers removes heartbeats nobody has refreshed for 5 minutes
// (the UI only shows those younger than 30 seconds).
func (s *Service) pruneViewers(ctx context.Context) {
	_, _ = s.pool.Exec(ctx, `DELETE FROM viewers WHERE seen_at < now() - interval '5 minutes'`)
}
