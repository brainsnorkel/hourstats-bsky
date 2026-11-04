package main

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// popPost builds a window post; hydrated posts carry an author handle.
func popPost(i int, hydrated bool) store.Post {
	p := store.Post{
		URI:  fmt.Sprintf("at://did:plc:test/app.bsky.feed.post/%d", i),
		Text: fmt.Sprintf("post %d is great", i),
	}
	if hydrated {
		p.AuthorHandle = fmt.Sprintf("user%d.bsky.social", i)
		p.Likes = i
	}
	return p
}

func uris(posts []store.Post) []string {
	out := make([]string, len(posts))
	for i, p := range posts {
		out[i] = p.URI
	}
	return out
}

func allTrue(n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = true
	}
	return out
}

func TestSelectPopulation(t *testing.T) {
	tests := []struct {
		name      string
		posts     []store.Post
		attempted []bool
		scored    []int // indices into posts, in Scored order
		ranked    []int
		hydrated  int
		never     int
		lost      int
		lostPct   float64
	}{
		{
			name:      "all hydrated",
			posts:     []store.Post{popPost(0, true), popPost(1, true), popPost(2, true)},
			attempted: allTrue(3),
			scored:    []int{0, 1, 2},
			ranked:    []int{0, 1, 2},
			hydrated:  3,
		},
		{
			name:      "second half never attempted",
			posts:     []store.Post{popPost(0, true), popPost(1, true), popPost(2, false), popPost(3, false)},
			attempted: []bool{true, true, false, false},
			scored:    []int{0, 1, 2, 3},
			ranked:    []int{0, 1},
			hydrated:  2,
			never:     2,
		},
		{
			name: "attempted-lost excluded from both",
			// 1 and 3 were attempted and came back missing; 4 and 5 never attempted.
			posts:     []store.Post{popPost(0, true), popPost(1, false), popPost(2, true), popPost(3, false), popPost(4, false), popPost(5, false)},
			attempted: []bool{true, true, true, true, false, false},
			scored:    []int{0, 2, 4, 5},
			ranked:    []int{0, 2},
			hydrated:  2,
			never:     2,
			lost:      2,
			lostPct:   50, // 2 of 4 attempted
		},
		{
			name: "hydrated posts lead Scored even when interleaved",
			// 0 never attempted, 1 hydrated: Ranked must stay a prefix of Scored.
			posts:     []store.Post{popPost(0, false), popPost(1, true)},
			attempted: []bool{false, true},
			scored:    []int{1, 0},
			ranked:    []int{1},
			hydrated:  1,
			never:     1,
		},
		{
			name:      "nil attempted counts every post as attempted",
			posts:     []store.Post{popPost(0, true), popPost(1, false), popPost(2, true), popPost(3, true)},
			attempted: nil,
			scored:    []int{0, 2, 3},
			ranked:    []int{0, 2, 3},
			hydrated:  3,
			lost:      1,
			lostPct:   25,
		},
		{
			name:      "nothing attempted",
			posts:     []store.Post{popPost(0, false), popPost(1, false)},
			attempted: []bool{false, false},
			scored:    []int{0, 1},
			ranked:    []int{},
			never:     2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pop := selectPopulation(tt.posts, tt.attempted)
			pick := func(idx []int) []string {
				out := make([]string, len(idx))
				for i, j := range idx {
					out[i] = tt.posts[j].URI
				}
				return out
			}
			if got, want := uris(pop.Scored), pick(tt.scored); !reflect.DeepEqual(got, want) {
				t.Errorf("Scored = %v, want %v", got, want)
			}
			if got, want := uris(pop.Ranked), pick(tt.ranked); !reflect.DeepEqual(got, want) {
				t.Errorf("Ranked = %v, want %v", got, want)
			}
			if pop.WindowPosts != len(tt.posts) || pop.Hydrated != tt.hydrated || pop.NeverAttempted != tt.never || pop.AttemptedLost != tt.lost {
				t.Errorf("counts = window %d hydrated %d never %d lost %d, want %d %d %d %d",
					pop.WindowPosts, pop.Hydrated, pop.NeverAttempted, pop.AttemptedLost,
					len(tt.posts), tt.hydrated, tt.never, tt.lost)
			}
			if math.Abs(pop.AttemptedLostPct-tt.lostPct) > 1e-9 {
				t.Errorf("AttemptedLostPct = %v, want %v", pop.AttemptedLostPct, tt.lostPct)
			}
		})
	}
}

// The 2026-11-04 03:55 hour: 193,985 posts, 145,543 hydrated, 47,385 never
// attempted, the rest attempted and lost.
func TestSelectPopulationElectionNightHour(t *testing.T) {
	const window, hydrated, never = 193985, 145543, 47385
	posts := make([]store.Post, window)
	attempted := make([]bool, window)
	attemptedCount := window - never
	for i := range posts {
		attempted[i] = i < attemptedCount
		if i < hydrated {
			posts[i].AuthorHandle = "h"
		}
	}
	pop := selectPopulation(posts, attempted)
	if pop.Hydrated != hydrated || pop.NeverAttempted != never || pop.AttemptedLost != 1057 {
		t.Errorf("hydrated %d never %d lost %d, want %d %d 1057", pop.Hydrated, pop.NeverAttempted, pop.AttemptedLost, hydrated, never)
	}
	if len(pop.Scored) != hydrated+never {
		t.Errorf("len(Scored) = %d, want %d", len(pop.Scored), hydrated+never)
	}
	if want := 1057.0 * 100 / 146600; math.Abs(pop.AttemptedLostPct-want) > 1e-9 || pop.AttemptedLostPct > defaultHydrationMaxErrorPct {
		t.Errorf("AttemptedLostPct = %v, want %v (under the default threshold)", pop.AttemptedLostPct, want)
	}
}

// On an hour where hydration completes, the population and its sentiment are
// exactly what the pre-hs-thk.4 AuthorHandle filter produced.
func TestSelectPopulationUntruncatedMatchesOldFilter(t *testing.T) {
	texts := []string{"I love this", "this is terrible", "meh", "wonderful day!", "awful news", "ok"}
	var posts []store.Post
	for i := 0; i < 600; i++ {
		p := popPost(i, i%7 != 3) // every seventh post came back missing
		p.Text = texts[i%len(texts)]
		p.IsReply = i%2 == 0
		posts = append(posts, p)
	}

	var old []store.Post
	for _, p := range posts {
		if p.AuthorHandle != "" {
			old = append(old, p)
		}
	}

	pop := selectPopulation(posts, allTrue(len(posts)))
	if !reflect.DeepEqual(pop.Scored, old) {
		t.Fatal("Scored differs from the old hydrated filter")
	}
	if !reflect.DeepEqual(pop.Ranked, old) {
		t.Fatal("Ranked differs from the old hydrated filter")
	}

	before, err := scoreWindow(toAnalyzerPosts(old), "test-old")
	if err != nil {
		t.Fatal(err)
	}
	after, err := scoreWindow(toAnalyzerPosts(pop.Scored), "test-new")
	if err != nil {
		t.Fatal(err)
	}
	if before.NetPct != after.NetPct || before.Category != after.Category ||
		before.RootPct != after.RootPct || before.ReplyPct != after.ReplyPct {
		t.Errorf("sentiment changed: before %+v after %+v", before, after)
	}
}

func TestHydrationMaxErrorPct(t *testing.T) {
	tests := []struct {
		name, newVal, oldVal string
		want                 int
	}{
		{"default", "", "", 10},
		{"new name", "7", "", 7},
		{"deprecated alias", "", "60", 60},
		{"new name wins over alias", "5", "60", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HYDRATION_MAX_ERROR_PCT", tt.newVal)
			t.Setenv("HYDRATION_MAX_UNHYDRATED_PCT", tt.oldVal)
			if got := hydrationMaxErrorPct(); got != tt.want {
				t.Errorf("hydrationMaxErrorPct() = %d, want %d", got, tt.want)
			}
		})
	}
}

// BenchmarkSelectPopulation300k sizes the population split at the window cap:
// one Scored slice of store.Post, Ranked sharing its backing array.
func BenchmarkSelectPopulation300k(b *testing.B) {
	const n = 300000
	posts := make([]store.Post, n)
	attempted := make([]bool, n)
	for i := range posts {
		posts[i] = store.Post{URI: fmt.Sprintf("at://did:plc:test/app.bsky.feed.post/%d", i), Text: "some post text"}
		attempted[i] = i < n*3/4
		if i < n*3/4-1000 {
			posts[i].AuthorHandle = "user.bsky.social"
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pop := selectPopulation(posts, attempted)
		if len(pop.Scored) == 0 {
			b.Fatal("empty population")
		}
	}
}
