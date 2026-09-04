package topics

import (
	"math"
	"sort"
	"strings"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// Exemplar ranking scores a candidate post by how much of a topic's *weighted*
// keyword set it covers, not by how many times it repeats one keyword. A post
// that says "school" and "football" six times between them is a worse exemplar
// for "College Football" than one that mentions five different topic keywords
// once each.
const (
	// weightCompound applies to bigram tokens ("georgia_tech"): a compound
	// match is near-proof the post is on topic.
	weightCompound = 3.0
	// weightAnchor applies to keywords that appear in the topic label.
	weightAnchor = 2.0
	// weightPlain applies to ordinary topic keywords.
	weightPlain = 1.0
	// weightGeneric applies to keywords common enough across the corpus that
	// matching them says almost nothing ("says", "games", "school").
	weightGeneric = 0.5
	// weightSynonym applies to synonyms, which are weaker evidence than the
	// keywords Gemini chose for the cluster.
	weightSynonym = 0.5

	// genericDocFreqRatio is the share of the corpus a term must appear in
	// before it is treated as generic when document-frequency data is
	// available.
	genericDocFreqRatio = 0.02

	// minRelevance is the minimum weighted keyword coverage a candidate needs.
	minRelevance = 0.25

	// minAnchorlessMatches is how many distinct keywords a candidate must match
	// for a topic whose keywords contain no anchor.
	minAnchorlessMatches = 2

	// qualityRootBoost favours root posts over replies, which usually read as
	// fragments out of context.
	qualityRootBoost = 1.15
	// qualityThinPenalty halves the score of very short or hashtag-stuffed posts.
	qualityThinPenalty = 0.5
	minExemplarWords   = 8
	maxExemplarHashes  = 3
)

// genericTerms is the fallback generic-word list used when no document
// frequency data is available for a keyword. These are words that trend
// constantly and therefore carry no topic signal on their own.
var genericTerms = map[string]bool{
	"says": true, "vote": true, "party": true, "court": true, "war": true,
	"school": true, "games": true, "news": true, "people": true, "today": true,
	"time": true, "year": true, "day": true, "live": true, "new": true,
	"back": true, "watch": true, "show": true,
}

// DocFreqStats carries the document frequencies measured by this cycle's
// TF-IDF pass so ranking can tell a genuinely distinctive keyword from one
// that is merely common right now. A nil *DocFreqStats falls back to
// genericTerms.
type DocFreqStats struct {
	DocFreq   map[string]int
	TotalDocs int
}

// newDocFreqStats builds DocFreqStats from TF-IDF term scores. Terms outside
// the TF-IDF cut are simply absent, and fall back to genericTerms.
func newDocFreqStats(terms []TermScore, totalDocs int) *DocFreqStats {
	if len(terms) == 0 || totalDocs <= 0 {
		return nil
	}
	df := make(map[string]int, len(terms))
	for _, t := range terms {
		df[t.Term] = t.DocFreq
	}
	return &DocFreqStats{DocFreq: df, TotalDocs: totalDocs}
}

// isGeneric reports whether a keyword is too common to be evidence of topic
// membership.
func (d *DocFreqStats) isGeneric(kw string) bool {
	if d != nil && d.TotalDocs > 0 {
		if n, ok := d.DocFreq[kw]; ok {
			return float64(n)/float64(d.TotalDocs) > genericDocFreqRatio
		}
	}
	return genericTerms[kw]
}

// keywordWeights holds the per-keyword evidence weights for one topic.
type keywordWeights struct {
	weights map[string]float64
	anchors map[string]bool
	total   float64
}

// buildKeywordWeights assigns an evidence weight to every keyword and synonym
// of a topic. Compound tokens and keywords named in the label are anchors:
// they are what makes a post about *this* topic rather than an adjacent one.
// Anchors are never downweighted as generic, because a word in the label is
// topical by definition.
func buildKeywordWeights(label string, keywords, synonyms []string, df *DocFreqStats) keywordWeights {
	kw := keywordWeights{
		weights: make(map[string]float64, len(keywords)+len(synonyms)),
		anchors: make(map[string]bool),
	}
	labelWords := splitLabelWords(label)

	for _, k := range keywords {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		var w float64
		switch {
		case strings.Contains(k, "_"):
			w = weightCompound
			kw.anchors[k] = true
		case isLabelAnchor(labelWords, k):
			w = weightAnchor
			kw.anchors[k] = true
		case df.isGeneric(k):
			w = weightGeneric
		default:
			w = weightPlain
		}
		kw.addWeight(k, w)
	}

	for _, s := range synonyms {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		kw.addWeight(s, weightSynonym)
	}

	return kw
}

// addWeight records the highest weight seen for a term (a term listed as both
// keyword and synonym keeps its keyword weight) and keeps total in sync.
func (k *keywordWeights) addWeight(term string, w float64) {
	if prev, ok := k.weights[term]; ok {
		if w <= prev {
			return
		}
		k.total += w - prev
		k.weights[term] = w
		return
	}
	k.weights[term] = w
	k.total += w
}

// splitLabelWords lowercases a topic label and splits it into alphanumeric words.
func splitLabelWords(label string) []string {
	return strings.FieldsFunc(strings.ToLower(label), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}

// isLabelAnchor reports whether a keyword names the topic itself. Matching is
// per label word rather than bare substring so that "war" does not anchor
// "Warriors"; keywords of 4+ characters may match a label word by prefix so
// that "tariff" still anchors "Tariffs".
func isLabelAnchor(labelWords []string, kw string) bool {
	for _, w := range labelWords {
		if w == kw {
			return true
		}
		if len(kw) >= 4 && strings.HasPrefix(w, kw) {
			return true
		}
	}
	return false
}

// distinctMatched returns the candidate's matched tokens deduplicated and
// restricted to this topic's keyword set.
func (k keywordWeights) distinctMatched(matched []string) []string {
	seen := make(map[string]bool, len(matched))
	out := make([]string, 0, len(matched))
	for _, m := range matched {
		m = strings.ToLower(strings.TrimSpace(m))
		if m == "" || seen[m] {
			continue
		}
		if _, ok := k.weights[m]; !ok {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// relevance is the share of the topic's total keyword weight that the
// candidate covers, in [0,1]. Repeated occurrences of the same keyword never
// add anything.
func (k keywordWeights) relevance(distinct []string) float64 {
	if k.total <= 0 {
		return 0
	}
	var sum float64
	for _, m := range distinct {
		sum += k.weights[m]
	}
	return sum / k.total
}

// meetsAnchorRule reports whether the candidate has enough topical evidence to
// be considered at all: at least one anchor when the topic has anchors,
// otherwise at least two distinct keywords.
func (k keywordWeights) meetsAnchorRule(distinct []string) bool {
	if len(k.anchors) > 0 {
		for _, m := range distinct {
			if k.anchors[m] {
				return true
			}
		}
		return false
	}
	return len(distinct) >= minAnchorlessMatches
}

// exemplarQuality scores a post as a thing to show a reader: engaged-with,
// standalone, and substantial. Engagement is logarithmic so a viral post
// cannot outweigh being off-topic.
func exemplarQuality(text string, engagement int, isReply bool) float64 {
	if engagement < 0 {
		engagement = 0
	}
	q := 1 + math.Log1p(float64(engagement))
	if !isReply {
		q *= qualityRootBoost
	}
	if len(strings.Fields(text)) < minExemplarWords || strings.Count(text, "#") > maxExemplarHashes {
		q *= qualityThinPenalty
	}
	return q
}

// rankedExemplar is a candidate with its computed ranking components.
type rankedExemplar struct {
	store.ExemplarCandidate
	Distinct  []string
	Relevance float64
	Quality   float64
	Score     float64
}

// rankExemplarCandidates filters candidates that are not credibly about the
// topic and orders the rest by relevance x quality.
func rankExemplarCandidates(label string, keywords, synonyms []string, candidates []store.ExemplarCandidate, df *DocFreqStats) []rankedExemplar {
	weights := buildKeywordWeights(label, keywords, synonyms, df)
	if weights.total <= 0 {
		return nil
	}

	ranked := make([]rankedExemplar, 0, len(candidates))
	for _, c := range candidates {
		if c.Handle == "" {
			continue
		}
		if IsRepetitive(c.Text) {
			continue
		}
		distinct := weights.distinctMatched(c.Matched)
		if !weights.meetsAnchorRule(distinct) {
			continue
		}
		rel := weights.relevance(distinct)
		if rel < minRelevance {
			continue
		}
		q := exemplarQuality(c.Text, c.Engagement, c.IsReply)
		ranked = append(ranked, rankedExemplar{
			ExemplarCandidate: c,
			Distinct:          distinct,
			Relevance:         rel,
			Quality:           q,
			Score:             rel * q,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Engagement != b.Engagement {
			return a.Engagement > b.Engagement
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt > b.CreatedAt
		}
		return a.URI < b.URI
	})
	return ranked
}
