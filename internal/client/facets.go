package client

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
	"github.com/bluesky-social/indigo/api/bsky"
)

// CreateWikipediaLinkFacets creates facets for Wikipedia link text in the post
// Looks for patterns like "Sep 18 events" or "Oct 10 events" and makes them clickable
// The URLs are no longer in the text, so we match the date + "events" pattern directly
func CreateWikipediaLinkFacets(text string) []*bsky.RichtextFacet {
	var facets []*bsky.RichtextFacet

	// Pattern to match: "Jan 2 events", "Sep 18 events", "Oct 10 events", etc.
	// Matches month abbreviation (3 letters) + space + day (1-2 digits) + space + "events"
	// Examples: "Sep 18 events", "Oct 10 events", "Jan 1 events"
	pattern := regexp.MustCompile(`\b([A-Z][a-z]{2} \d{1,2} events)\b`)
	matches := pattern.FindAllStringSubmatchIndex(text, -1)

	// For each match, we need to find the corresponding Wikipedia URL
	// Since URLs are no longer in the text, we'll need to generate them from the date
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}

		// Extract the matched text (e.g., "Sep 18 events")
		matchedText := text[match[2]:match[3]]
		
		// Extract the date portion (e.g., "Sep 18")
		datePortion := matchedText[:len(matchedText)-7] // Remove " events" (7 chars)

		// Parse the date to generate the Wikipedia URL
		// We need to determine the year - check the post text for a date range to infer the year
		// Format: "Bluesky Sentiment YYYY-MM-DD - YYYY-MM-DD"
		var year int
		yearPattern := regexp.MustCompile(`Bluesky Sentiment (\d{4})-\d{2}-\d{2}`)
		yearMatch := yearPattern.FindStringSubmatch(text)
		if len(yearMatch) >= 2 {
			// Try to parse the year from the title
			if parsedYear, err := strconv.Atoi(yearMatch[1]); err == nil {
				year = parsedYear
			} else {
				year = time.Now().Year()
			}
		} else {
			// Fallback to current year
			year = time.Now().Year()
		}
		
		dateStr := fmt.Sprintf("%s %d", datePortion, year)
		
		// Try to parse the date
		date, err := time.Parse("Jan 2 2006", dateStr)
		if err != nil {
			// If parsing fails, try with different format or skip
			continue
		}

		// Generate Wikipedia URL for this date
		monthName := date.Format("January")
		day := date.Day()
		wikiURL := fmt.Sprintf("https://en.wikipedia.org/wiki/Portal:Current_events/%s_%d#%d_%s_%d",
			monthName, year, year, monthName, day)

		// Create a link facet for the entire date + "events" phrase
		facet := &bsky.RichtextFacet{
			Index: &bsky.RichtextFacet_ByteSlice{
				ByteStart: int64(match[2]),
				ByteEnd:   int64(match[3]),
			},
			Features: []*bsky.RichtextFacet_Features_Elem{
				{
					RichtextFacet_Link: &bsky.RichtextFacet_Link{
						Uri: wikiURL,
					},
				},
			},
		}

		facets = append(facets, facet)
	}

	// No fallback needed - we always try to match the date pattern

	return facets
}

