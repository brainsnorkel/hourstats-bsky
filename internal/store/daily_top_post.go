package store

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) GetTopPostForDate(ctx context.Context, date string) (*Post, error) {
	dayStart := date + "T00:00:00Z"
	parsed, err := time.Parse(time.RFC3339, dayStart)
	if err != nil {
		return nil, fmt.Errorf("parse date %q: %w", date, err)
	}
	dayEnd := parsed.Add(24 * time.Hour).Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx,
		`SELECT top_posts FROM runs WHERE created_at >= ? AND created_at < ?`,
		dayStart, dayEnd,
	)
	if err != nil {
		return nil, fmt.Errorf("query runs for date %s: %w", date, err)
	}
	defer rows.Close()

	var best *Post
	for rows.Next() {
		var topPostsJSON string
		if err := rows.Scan(&topPostsJSON); err != nil {
			continue
		}
		posts := unmarshalPosts(topPostsJSON)
		for i := range posts {
			if posts[i].URI == "" || posts[i].CID == "" {
				continue
			}
			if best == nil || posts[i].EngagementScore > best.EngagementScore {
				p := posts[i]
				best = &p
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs for date %s: %w", date, err)
	}

	return best, nil
}
