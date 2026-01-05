package client

import (
	"fmt"
	"regexp"
	"time"

	"github.com/bluesky-social/indigo/api/bsky"
)

// EventDate represents a date for Wikipedia link generation
type EventDate struct {
	DisplayText string // e.g., "Jan 3" - text that appears in the post
	FullDate    string // e.g., "2026-01-03" - full date with year for URL generation
}

// CreateWikipediaLinkFacets creates facets for Wikipedia link text in the post
// Looks for patterns like "Sep 18 events" or "Oct 10 events" and makes them clickable
// eventDates is an optional slice of EventDate structs that provide the actual dates with years
// for accurate Wikipedia URL generation when the date range spans multiple years
func CreateWikipediaLinkFacets(text string, eventDates ...EventDate) []*bsky.RichtextFacet {
	var facets []*bsky.RichtextFacet

	// Build a map of display text to full date for quick lookup
	dateMap := make(map[string]string)
	for _, ed := range eventDates {
		dateMap[ed.DisplayText] = ed.FullDate
	}

	// Pattern to match: "Jan 2 events", "Sep 18 events", "Oct 10 events", etc.
	// Matches month abbreviation (3 letters) + space + day (1-2 digits) + space + "events"
	// Examples: "Sep 18 events", "Oct 10 events", "Jan 1 events"
	pattern := regexp.MustCompile(`\b([A-Z][a-z]{2} \d{1,2}) events\b`)
	matches := pattern.FindAllStringSubmatchIndex(text, -1)

	for _, match := range matches {
		if len(match) < 4 {
			continue
		}

		// match[0]:match[1] is the full match (e.g., "Jan 3 events")
		// match[2]:match[3] is the captured group (e.g., "Jan 3")
		datePortion := text[match[2]:match[3]] // e.g., "Jan 3"

		var year int
		var date time.Time

		// First, check if we have an exact date provided in eventDates
		if fullDate, ok := dateMap[datePortion]; ok {
			// Parse the full date to get the correct year
			parsedDate, err := time.Parse("2006-01-02", fullDate)
			if err == nil {
				date = parsedDate
				year = parsedDate.Year()
			}
		}

		// If no exact date found, fall back to parsing from text
		if year == 0 {
			// Try to extract year from the title: "Bluesky Sentiment YYYY-MM-DD - YYYY-MM-DD"
			dateRangePattern := regexp.MustCompile(`Bluesky Sentiment (\d{4})-(\d{2})-\d{2} - (\d{4})-(\d{2})-\d{2}`)
			dateRangeMatch := dateRangePattern.FindStringSubmatch(text)

			if len(dateRangeMatch) >= 5 {
				startYear, _ := parseIntSafe(dateRangeMatch[1])
				startMonth, _ := parseIntSafe(dateRangeMatch[2])
				endYear, _ := parseIntSafe(dateRangeMatch[3])
				endMonth, _ := parseIntSafe(dateRangeMatch[4])

				// Parse just the month from the date portion to determine which year
				eventDate, err := time.Parse("Jan 2", datePortion)
				if err == nil {
					eventMonth := int(eventDate.Month())

					// Determine which year this event belongs to
					if startYear != endYear {
						// If event month >= start month, it's in the start year
						// If event month <= end month (and different from start), it's in the end year
						if eventMonth >= startMonth {
							year = startYear
						} else if eventMonth <= endMonth {
							year = endYear
						} else {
							year = endYear // Fallback
						}
					} else {
						year = startYear
					}

					// Create full date for URL generation
					date = time.Date(year, time.Month(eventMonth), eventDate.Day(), 0, 0, 0, 0, time.UTC)
				}
			}

			// Ultimate fallback: use current year
			if year == 0 {
				year = time.Now().Year()
				parsedDate, err := time.Parse("Jan 2 2006", fmt.Sprintf("%s %d", datePortion, year))
				if err != nil {
					continue
				}
				date = parsedDate
			}
		}

		// Generate Wikipedia URL for this date
		monthName := date.Format("January")
		day := date.Day()
		wikiURL := fmt.Sprintf("https://en.wikipedia.org/wiki/Portal:Current_events/%s_%d#%d_%s_%d",
			monthName, year, year, monthName, day)

		// Create a link facet for the entire date + "events" phrase
		facet := &bsky.RichtextFacet{
			Index: &bsky.RichtextFacet_ByteSlice{
				ByteStart: int64(match[0]), // Start of full match "Jan 3 events"
				ByteEnd:   int64(match[1]), // End of full match
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

	return facets
}

// parseIntSafe parses a string to int, returning 0 on error
func parseIntSafe(s string) (int, bool) {
	var result int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		result = result*10 + int(c-'0')
	}
	return result, true
}

