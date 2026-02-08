package topics

import (
	"context"
	"log"
	"time"

	"github.com/bluesky-social/indigo/api/bsky"
)

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

	for i, topic := range result {
		allKeywords := append(topic.Cluster.Keywords, topic.Cluster.Synonyms...)
		if len(allKeywords) == 0 {
			continue
		}

		uris, err := h.store.GetTopicTokenURIsByKeywords(ctx, allKeywords, cutoff, 50)
		if err != nil {
			log.Printf("exemplar: query URIs for %q: %v", topic.Cluster.Label, err)
			continue
		}
		if len(uris) == 0 {
			log.Printf("exemplar: no matching URIs for %q", topic.Cluster.Label)
			continue
		}

		var bestURI, bestHandle string
		bestEngagement := -1

		for start := 0; start < len(uris); start += exemplarBatchSize {
			end := start + exemplarBatchSize
			if end > len(uris) {
				end = len(uris)
			}

			views, err := h.fetcher.GetPosts(ctx, uris[start:end])
			if err != nil {
				log.Printf("exemplar: fetch posts for %q: %v", topic.Cluster.Label, err)
				continue
			}

			for _, v := range views {
				if v == nil {
					continue
				}
				eng := postEngagement(v)
				if eng > bestEngagement {
					bestEngagement = eng
					bestURI = v.Uri
					if v.Author != nil {
						bestHandle = v.Author.Handle
					}
				}
			}
		}

		if bestURI != "" {
			result[i].ExemplarURI = bestURI
			result[i].ExemplarHandle = bestHandle
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
