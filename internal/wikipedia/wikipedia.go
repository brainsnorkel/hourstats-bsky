// Package wikipedia builds links to Wikipedia pages the bot cites in posts.
package wikipedia

import (
	"fmt"
	"time"
)

// CurrentEventsDayURL returns the Wikipedia "current events" page for a single
// day, e.g. https://en.wikipedia.org/wiki/Portal:Current_events/2026_September_4.
// The per-day subpage is more durable than an anchor into the monthly page,
// whose fragment id exists in both a zero-padded and an unpadded form. The
// date is taken in UTC and the day is not padded.
func CurrentEventsDayURL(t time.Time) string {
	u := t.UTC()
	return fmt.Sprintf("https://en.wikipedia.org/wiki/Portal:Current_events/%d_%s_%d",
		u.Year(), u.Format("January"), u.Day())
}
