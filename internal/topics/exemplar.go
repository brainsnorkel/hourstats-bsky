package topics

import (
	"context"
	"log"
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
				if v == nil || v.Author == nil {
					continue
				}
				if hasAdultLabel(v.Labels) {
					continue
				}
				if usedHandles[v.Author.Handle] {
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
