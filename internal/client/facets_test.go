package client

import (
	"strings"
	"testing"
)

func TestParseIntSafe(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantVal int
		wantOK  bool
	}{
		{"valid number", "123", 123, true},
		{"zero", "0", 0, true},
		{"single digit", "7", 7, true},
		{"large number", "2026", 2026, true},
		{"empty string", "", 0, true},
		{"non-digits", "12a3", 0, false},
		{"all letters", "abc", 0, false},
		{"leading space", " 123", 0, false},
		{"negative sign", "-5", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok := parseIntSafe(tt.input)
			if val != tt.wantVal || ok != tt.wantOK {
				t.Errorf("parseIntSafe(%q) = (%d, %v), want (%d, %v)",
					tt.input, val, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

func TestCreateWikipediaLinkFacets(t *testing.T) {
	t.Run("single date events pattern", func(t *testing.T) {
		text := "Bluesky Sentiment 2026-01-01 - 2026-01-31\n\nLowest: -5% Jan 3 events\nHighest: 15% Jan 20 events"
		facets := CreateWikipediaLinkFacets(text)

		if len(facets) != 2 {
			t.Fatalf("got %d facets, want 2", len(facets))
		}

		for i, want := range []string{"2026_January_3", "2026_January_20"} {
			f := facets[i]
			if f.Features[0].RichtextFacet_Link == nil {
				t.Fatal("expected link facet")
			}
			uri := f.Features[0].RichtextFacet_Link.Uri
			if uri != "https://en.wikipedia.org/wiki/Portal:Current_events/"+want {
				t.Errorf("unexpected Wikipedia URL: %q", uri)
			}
			if strings.Contains(uri, "#") {
				t.Errorf("day page URL should carry no anchor: %q", uri)
			}
		}
	})

	t.Run("no matching pattern", func(t *testing.T) {
		text := "This text has no date events patterns"
		facets := CreateWikipediaLinkFacets(text)
		if len(facets) != 0 {
			t.Errorf("got %d facets, want 0", len(facets))
		}
	})

	t.Run("with explicit EventDates", func(t *testing.T) {
		text := "Lowest: -5% Jan 3 events"
		eventDates := []EventDate{
			{DisplayText: "Jan 3", FullDate: "2026-01-03"},
		}
		facets := CreateWikipediaLinkFacets(text, eventDates...)

		if len(facets) != 1 {
			t.Fatalf("got %d facets, want 1", len(facets))
		}

		uri := facets[0].Features[0].RichtextFacet_Link.Uri
		if !strings.Contains(uri, "Portal:Current_events/2026_January_3") {
			t.Errorf("expected the 2026_January_3 day page, got %q", uri)
		}
		if strings.Contains(uri, "#") {
			t.Errorf("day page URL should carry no anchor: %q", uri)
		}
	})

	t.Run("cross-year date range", func(t *testing.T) {
		text := "Bluesky Sentiment 2025-11-01 - 2026-01-31\n\nLowest: -5% Nov 15 events\nHighest: 15% Jan 10 events"
		eventDates := []EventDate{
			{DisplayText: "Nov 15", FullDate: "2025-11-15"},
			{DisplayText: "Jan 10", FullDate: "2026-01-10"},
		}
		facets := CreateWikipediaLinkFacets(text, eventDates...)

		if len(facets) != 2 {
			t.Fatalf("got %d facets, want 2", len(facets))
		}

		novURI := facets[0].Features[0].RichtextFacet_Link.Uri
		if !strings.Contains(novURI, "Portal:Current_events/2025_November_15") {
			t.Errorf("expected 2025_November_15, got %q", novURI)
		}

		janURI := facets[1].Features[0].RichtextFacet_Link.Uri
		if !strings.Contains(janURI, "Portal:Current_events/2026_January_10") {
			t.Errorf("expected 2026_January_10, got %q", janURI)
		}
	})

	t.Run("byte positions are correct", func(t *testing.T) {
		text := "Jan 3 events"
		facets := CreateWikipediaLinkFacets(text)
		if len(facets) != 1 {
			t.Fatalf("got %d facets, want 1", len(facets))
		}
		if facets[0].Index.ByteStart != 0 {
			t.Errorf("ByteStart = %d, want 0", facets[0].Index.ByteStart)
		}
		if facets[0].Index.ByteEnd != int64(len("Jan 3 events")) {
			t.Errorf("ByteEnd = %d, want %d", facets[0].Index.ByteEnd, len("Jan 3 events"))
		}
	})
}
