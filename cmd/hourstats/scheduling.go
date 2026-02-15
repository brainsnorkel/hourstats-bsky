package main

import (
	"log/slog"
	"time"
)

// newWallClockTicker returns a channel that fires at wall-clock aligned UTC
// boundaries. For example, a 30m interval fires at :00 and :30 past the hour;
// a 3h interval fires at 00:00, 03:00, 06:00, etc. This ensures deploys and
// restarts don't shift the posting schedule.
func newDailyTickerAtHour(hour int) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
			if !next.After(now) {
				next = next.AddDate(0, 0, 1)
			}
			delay := next.Sub(now)
			slog.Info("daily ticker scheduled",
				"hour_utc", hour,
				"next_fire", next.Format(time.RFC3339),
				"delay", delay.Round(time.Second),
			)
			timer := time.NewTimer(delay)
			<-timer.C
			ch <- time.Now()
		}
	}()
	return ch
}

func newWallClockTicker(interval, offset time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		for {
			now := time.Now().UTC()
			next := now.Truncate(interval).Add(offset)
			if !next.After(now) {
				next = next.Add(interval)
			}
			delay := next.Sub(now)
			slog.Info("wall-clock ticker scheduled",
				"interval", interval,
				"offset", offset,
				"next_fire", next.Format(time.RFC3339),
				"delay", delay.Round(time.Second),
			)
			timer := time.NewTimer(delay)
			<-timer.C
			ch <- time.Now()
		}
	}()
	return ch
}
