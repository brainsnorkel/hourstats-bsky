package topics

import (
	"context"
	"log/slog"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
)

var adultLabels = map[string]bool{
	"porn":          true,
	"sexual":        true,
	"nudity":        true,
	"graphic-media": true,
}

type ExemplarPostFetcher interface {
	GetPosts(ctx context.Context, uris []string) ([]*bsky.FeedDefs_PostView, error)
}

type ExemplarTokenStore interface {
	GetTopicTokenURIsByKeywords(ctx context.Context, keywords []string, cutoff string, limit int) ([]string, error)
}

type ExemplarHydrator struct {
	fetcher ExemplarPostFetcher
	store   ExemplarTokenStore
}

func NewExemplarHydrator(fetcher ExemplarPostFetcher, store ExemplarTokenStore) *ExemplarHydrator {
	return &ExemplarHydrator{fetcher: fetcher, store: store}
}

const exemplarBatchSize = 25

func (h *ExemplarHydrator) HydrateExemplars(ctx context.Context, topics []IdentifiedTopic) ([]IdentifiedTopic, error) {
	if len(topics) == 0 {
		return topics, nil
	}

	cutoff := time.Now().UTC().Add(-6 * time.Hour).Format(time.RFC3339)
	result := make([]IdentifiedTopic, len(topics))
	copy(result, topics)

	usedHandles := make(map[string]bool)

	for i, topic := range result {
		allKeywords := append(topic.Cluster.Keywords, topic.Cluster.Synonyms...)
		if len(allKeywords) == 0 {
			slog.Info("exemplar: no keywords", "topic", topic.Cluster.Label)
			continue
		}

		uris, err := h.store.GetTopicTokenURIsByKeywords(ctx, allKeywords, cutoff, 50)
		if err != nil {
			slog.Warn("exemplar: query URIs failed", "topic", topic.Cluster.Label, "error", err)
			continue
		}
		if len(uris) == 0 {
			slog.Warn("exemplar: no matching URIs", "topic", topic.Cluster.Label, "keywords", allKeywords)
			continue
		}
		slog.Info("exemplar: found candidate URIs", "topic", topic.Cluster.Label, "count", len(uris))

		var bestURI, bestHandle string
		bestEngagement := -1
		var adultSkipped, usedSkipped, nilSkipped, fetchErrors int

		for start := 0; start < len(uris); start += exemplarBatchSize {
			end := start + exemplarBatchSize
			if end > len(uris) {
				end = len(uris)
			}

			views, err := h.fetcher.GetPosts(ctx, uris[start:end])
			if err != nil {
				slog.Warn("exemplar: fetch posts failed", "topic", topic.Cluster.Label, "batch", start/exemplarBatchSize, "error", err)
				fetchErrors++
				continue
			}

			for _, v := range views {
				if v == nil || v.Author == nil {
					nilSkipped++
					continue
				}
				if hasAdultLabel(v.Labels) {
					adultSkipped++
					continue
				}
				if usedHandles[v.Author.Handle] {
					usedSkipped++
					continue
				}
				eng := postEngagement(v)
				if eng > bestEngagement {
					bestEngagement = eng
					bestURI = v.Uri
					bestHandle = v.Author.Handle
				}
			}

			if bestURI != "" {
				break
			}
		}

		if bestURI != "" {
			result[i].ExemplarURI = bestURI
			result[i].ExemplarHandle = bestHandle
			usedHandles[bestHandle] = true
			slog.Info("exemplar: selected", "topic", topic.Cluster.Label, "handle", bestHandle, "engagement", bestEngagement)
		} else {
			slog.Warn("exemplar: no valid candidate found", "topic", topic.Cluster.Label,
				"uris_checked", len(uris), "nil_skipped", nilSkipped,
				"adult_skipped", adultSkipped, "used_skipped", usedSkipped,
				"fetch_errors", fetchErrors)
		}
	}

	return result, nil
}

func postEngagement(v *bsky.FeedDefs_PostView) int {
	var total int
	if v.LikeCount != nil {
		total += int(*v.LikeCount)
	}
	if v.RepostCount != nil {
		total += int(*v.RepostCount)
	}
	if v.ReplyCount != nil {
		total += int(*v.ReplyCount)
	}
	return total
}

func hasAdultLabel(labels []*atproto.LabelDefs_Label) bool {
	for _, l := range labels {
		if l != nil && adultLabels[l.Val] {
			return true
		}
	}
	return false
}
