package observation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

func (s *ServerService) RecordMetrics(ctx context.Context, at time.Time, metrics domain.ServerMetrics) error {
	if err := validateObservedServerMetrics(at, metrics); err != nil {
		return err
	}
	at = at.UTC()
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()

	latest, err := s.repository.LatestServerMetricObservation(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("record observed server metrics: read durable baseline: %w", err)
	}
	if err == nil {
		s.metrics = serverMetricBaseline{valid: true, at: latest.At, uptime: latest.Metrics.UptimeSeconds}
		switch {
		case at.Before(latest.At):
			return fmt.Errorf("record observed server metrics: observation time %s is before durable baseline %s", at.Format(time.RFC3339Nano), latest.At.Format(time.RFC3339Nano))
		case at.Equal(latest.At):
			if latest.Metrics != metrics {
				return fmt.Errorf("record observed server metrics: observation at %s does not match durable sample", at.Format(time.RFC3339Nano))
			}
			if replayErr := s.repository.RecordServerMetricObservation(ctx, store.ServerMetricObservation{At: at, Metrics: metrics, Event: latest.Event}); replayErr != nil {
				return fmt.Errorf("record observed server metrics: prove durable replay: %w", replayErr)
			}
			return nil
		}
	}

	write := store.ServerMetricObservation{At: at, Metrics: metrics}
	if err == nil && metrics.UptimeSeconds < latest.Metrics.UptimeSeconds {
		event, eventErr := s.newRestartEvent(at, metrics.UptimeSeconds, latest.Metrics.UptimeSeconds)
		if eventErr != nil {
			return eventErr
		}
		write.Event = &event
	}
	if err := s.repository.RecordServerMetricObservation(ctx, write); err != nil {
		return fmt.Errorf("record observed server metrics: %w", err)
	}
	s.metrics = serverMetricBaseline{valid: true, at: at, uptime: metrics.UptimeSeconds}
	return nil
}

func validateObservedServerMetrics(at time.Time, metrics domain.ServerMetrics) error {
	if at.IsZero() {
		return fmt.Errorf("record observed server metrics: observation time is zero")
	}
	if metrics.ServerFPS < 0 || metrics.CurrentPlayerNum < 0 || metrics.MaxPlayerNum < 0 ||
		metrics.UptimeSeconds < 0 || metrics.BaseCampNum < 0 || metrics.Days < 0 {
		return fmt.Errorf("record observed server metrics: metric counts and uptime must be nonnegative")
	}
	if math.IsNaN(metrics.ServerFrameTime) || math.IsInf(metrics.ServerFrameTime, 0) || metrics.ServerFrameTime < 0 {
		return fmt.Errorf("record observed server metrics: server frame time must be finite and nonnegative")
	}
	return nil
}

func (s *ServerService) newRestartEvent(at time.Time, newUptime, oldUptime int64) (store.ActivityEvent, error) {
	payload, err := json.Marshal(struct {
		Old int64 `json:"old_uptime_seconds"`
		New int64 `json:"new_uptime_seconds"`
	}{Old: oldUptime, New: newUptime})
	if err != nil {
		return store.ActivityEvent{}, fmt.Errorf("record observed server restart: encode payload: %w", err)
	}
	return s.newEvent("server_restarted", at, payload)
}
