package topics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultGeminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"
	maxDailyCalls         = 100
)

// Grouper calls Google Gemini Flash to group TF-IDF terms into topic clusters.
type Grouper struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	mu         sync.Mutex
	dailyCalls int
	lastReset  time.Time
}

// NewGrouper creates a Grouper using the default Gemini endpoint.
func NewGrouper(apiKey string) *Grouper {
	return &Grouper{
		apiKey:   apiKey,
		endpoint: defaultGeminiEndpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		lastReset: time.Now(),
	}
}

// NewGrouperWithEndpoint creates a Grouper with a custom endpoint (for testing).
func NewGrouperWithEndpoint(apiKey, endpoint string) *Grouper {
	return &Grouper{
		apiKey:   apiKey,
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		lastReset: time.Now(),
	}
}

// geminiRequest is the request body for Gemini Flash.
type geminiRequest struct {
	Contents         []geminiContent `json:"contents"`
	GenerationConfig geminiGenConfig `json:"generationConfig"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text    string `json:"text"`
	Thought bool   `json:"thought,omitempty"`
}

type geminiGenConfig struct {
	ResponseMimeType string                 `json:"responseMimeType"`
	ThinkingConfig   *geminiThinkingConfig  `json:"thinkingConfig,omitempty"`
	ResponseSchema   map[string]interface{} `json:"responseSchema,omitempty"`
}

type geminiThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

// topicClusterSchema defines the JSON schema for structured output from Gemini.
var topicClusterSchema = map[string]interface{}{
	"type": "ARRAY",
	"items": map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"label":         map[string]interface{}{"type": "STRING"},
			"description":   map[string]interface{}{"type": "STRING"},
			"keywords":      map[string]interface{}{"type": "ARRAY", "items": map[string]interface{}{"type": "STRING"}},
			"synonyms":      map[string]interface{}{"type": "ARRAY", "items": map[string]interface{}{"type": "STRING"}},
			"justification": map[string]interface{}{"type": "STRING"},
		},
		"required": []string{"label", "description", "keywords", "synonyms", "justification"},
	},
}

// geminiResponse is the response body from Gemini Flash.
type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

// GroupAndLabel sends the top TF-IDF terms to Gemini Flash and returns grouped clusters.
// Falls back to single-keyword clusters if the API call fails or is rate-limited.
func (g *Grouper) GroupAndLabel(ctx context.Context, terms []TermScore) ([]TopicCluster, error) {
	if len(terms) == 0 {
		return nil, nil
	}

	if !g.checkAndIncrementRate() {
		log.Printf("grouper: daily rate limit (%d) reached, using fallback", maxDailyCalls)
		return fallbackClusters(terms), nil
	}

	prompt := buildPrompt(terms)

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
		},
		GenerationConfig: geminiGenConfig{
			ResponseMimeType: "application/json",
			ThinkingConfig:   &geminiThinkingConfig{ThinkingBudget: 1024},
			ResponseSchema:   topicClusterSchema,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fallbackClusters(terms), fmt.Errorf("grouper: marshal request: %w", err)
	}

	url := g.endpoint + "?key=" + g.apiKey
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return fallbackClusters(terms), fmt.Errorf("grouper: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("grouper: API call failed: %v, using fallback", err)
		return fallbackClusters(terms), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("grouper: API returned %d: %s, using fallback", resp.StatusCode, string(respBody))
		return fallbackClusters(terms), nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("grouper: read response: %v, using fallback", err)
		return fallbackClusters(terms), nil
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		log.Printf("grouper: unmarshal response: %v, using fallback", err)
		return fallbackClusters(terms), nil
	}

	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		log.Printf("grouper: empty response from Gemini, using fallback")
		return fallbackClusters(terms), nil
	}

	jsonText := extractResponseText(gemResp.Candidates[0].Content.Parts)
	if jsonText == "" {
		log.Printf("grouper: no response text in Gemini output, using fallback")
		return fallbackClusters(terms), nil
	}

	var clusters []TopicCluster
	if err := json.Unmarshal([]byte(jsonText), &clusters); err != nil {
		log.Printf("grouper: parse clusters JSON: %v, using fallback", err)
		return fallbackClusters(terms), nil
	}

	for _, c := range clusters {
		log.Printf("grouper: topic %q — %s", c.Label, c.Justification)
	}

	if len(clusters) > MaxLLMGroups {
		clusters = clusters[:MaxLLMGroups]
	}

	clusters = filterGenericClusters(clusters)
	return clusters, nil
}

var genericLabelWords = map[string]bool{
	"miscellaneous": true, "general": true, "various": true, "other": true,
	"everyday": true, "mixed": true, "assorted": true, "unrelated": true,
	"uncategorized": true, "uncategorised": true, "unclassified": true,
	"activities": true, "actions": true, "terms": true, "words": true,
	"posts": true, "mentions": true, "discussions": true, "content": true,
	"topics": true, "updates": true, "community": true, "online": true,
	"opinions": true, "reactions": true, "criticism": true, "takes": true,
	"views": true, "thoughts": true, "responses": true, "debate": true,
	"controversy": true, "discourse": true, "random": true, "randomly": true,
	"entertainment": true, "politics": true, "culture": true, "platforms": true, "social": true,
	"movie": true, "movies": true, "events": true, "current": true, "news": true,
	"media": true, "quality": true, "language": true,
}

func extractResponseText(parts []geminiPart) string {
	for _, p := range parts {
		if !p.Thought && p.Text != "" {
			return p.Text
		}
	}
	return ""
}

func filterGenericClusters(clusters []TopicCluster) []TopicCluster {
	var filtered []TopicCluster
	for _, c := range clusters {
		lower := strings.ToLower(strings.TrimSpace(c.Label))
		if lower == "__discard__" {
			continue
		}
		words := strings.Fields(lower)
		generic := false
		for _, w := range words {
			if genericLabelWords[w] {
				generic = true
				break
			}
		}
		if !generic {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

type detectedPhrase struct {
	Phrase string
	Terms  []string
}

// detectOverlappingPhrases chains compound terms that share a boundary word
// (e.g. post_banger + banger_that → "post banger that") into reconstructed
// viral phrases for the LLM.
func detectOverlappingPhrases(terms []TermScore) []detectedPhrase {
	type compoundTerm struct {
		term  string
		words []string
	}
	var compounds []compoundTerm
	for _, t := range terms {
		if parts := strings.Split(t.Term, "_"); len(parts) >= 2 {
			compounds = append(compounds, compoundTerm{term: t.Term, words: parts})
		}
	}
	if len(compounds) < 2 {
		return nil
	}

	byFirst := map[string][]compoundTerm{}
	for _, c := range compounds {
		byFirst[c.words[0]] = append(byFirst[c.words[0]], c)
	}

	used := map[string]bool{}
	var phrases []detectedPhrase

	for _, start := range compounds {
		if used[start.term] {
			continue
		}
		chain := []compoundTerm{start}
		current := start
		for {
			lastWord := current.words[len(current.words)-1]
			nexts, ok := byFirst[lastWord]
			if !ok {
				break
			}
			found := false
			for _, next := range nexts {
				if next.term != current.term && !used[next.term] {
					chain = append(chain, next)
					current = next
					found = true
					break
				}
			}
			if !found {
				break
			}
		}
		if len(chain) < 2 {
			continue
		}
		merged := make([]string, len(chain[0].words))
		copy(merged, chain[0].words)
		var termNames []string
		for i, c := range chain {
			termNames = append(termNames, c.term)
			if i > 0 {
				merged = append(merged, c.words[1:]...)
			}
		}
		phrase := strings.Join(merged, " ")
		for _, c := range chain {
			used[c.term] = true
		}
		phrases = append(phrases, detectedPhrase{Phrase: phrase, Terms: termNames})
	}
	return phrases
}

func buildPrompt(terms []TermScore) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Group these %d TF-IDF terms from recent Bluesky posts into trending topics.\n\n", len(terms))
	b.WriteString("Terms (score):\n")
	for _, t := range terms {
		fmt.Fprintf(&b, "- %s (%.2f)\n", t.Term, t.Score)
	}

	if phrases := detectOverlappingPhrases(terms); len(phrases) > 0 {
		b.WriteString("\nDETECTED PHRASES (from overlapping compound terms):\n")
		for _, p := range phrases {
			fmt.Fprintf(&b, "- \"%s\" (from terms: %s)\n", p.Phrase, strings.Join(p.Terms, " + "))
		}
		b.WriteString("These are likely viral phrases or meme formats being repeated across many posts. Each detected phrase should become a SINGLE topic. Use the phrase (or a recognisable shortened form) as the label.\n")
	}

	b.WriteString("\nTOPIC QUALITY:\n")
	b.WriteString("- Labels must name a SPECIFIC subject: a person, event, place, organisation, law, product, or concrete phenomenon.\n")
	b.WriteString("- Good labels: \"Donald Trump\", \"Super Bowl\", \"Age Verification\", \"Hurricane Milton\", \"Taylor Swift\", \"Epstein Files\", \"Post a Banger\"\n")
	b.WriteString("- Bad labels: \"Entertainment\", \"Social Media\", \"Politics\", \"Current Events\", \"Movie\", \"Random\", \"Online Discourse\", \"Banger Posts\"\n")
	b.WriteString("- Test: if a label could be a newspaper SECTION header, it is too broad. Labels should be specific enough to be a newspaper HEADLINE.\n")
	b.WriteString("- A viral phrase or meme format that many users are repeating IS a specific topic — treat it like an event, not meta-commentary.\n\n")
	b.WriteString("GROUPING:\n")
	b.WriteString("- Aggressively merge related terms into the most recognizable event, person, or subject.\n")
	b.WriteString("- If a major event is happening (e.g. Super Bowl, Oscars), ALL related terms (teams, players, performers, venues) MUST merge into that single event.\n")
	b.WriteString("- Terms with underscores are multi-word phrases (bad_bunny = \"Bad Bunny\", super_bowl = \"Super Bowl\"). Use multi-word form in labels.\n")
	b.WriteString("- When compound terms share a word (e.g. post_banger + banger_that), they are fragments of the same viral phrase. Merge ALL overlapping fragments into ONE topic.\n")
	b.WriteString("- Aim for 5-7 distinct topics. Fewer strong topics is better than more weak ones.\n")
	b.WriteString("- Maximum 10 groups. Every input term must appear in exactly one group's keywords.\n\n")
	b.WriteString("DISCARD:\n")
	b.WriteString("- Group vague, generic, or meta-commentary terms under the label \"__discard__\".\n")
	b.WriteString("- This includes: reaction words, mood words, generic adjectives, medium/format descriptions, and anything that describes HOW people are talking rather than WHAT they are talking about.\n")
	b.WriteString("- It is better to discard borderline terms than to create a weak topic.\n\n")
	b.WriteString("JUSTIFICATION:\n")
	b.WriteString("- For each topic, provide a brief justification explaining why these terms form a coherent topic and why the label is specific enough.\n")
	b.WriteString("- For __discard__, explain why the terms are too vague or generic to form a real topic.\n")
	return b.String()
}

// fallbackClusters creates single-keyword clusters from the top terms.
func fallbackClusters(terms []TermScore) []TopicCluster {
	n := len(terms)
	if n > TopTopics {
		n = TopTopics
	}
	clusters := make([]TopicCluster, n)
	for i := 0; i < n; i++ {
		label := strings.ToUpper(terms[i].Term[:1]) + terms[i].Term[1:]
		clusters[i] = TopicCluster{
			Label:       label,
			Description: "Trending term",
			Keywords:    []string{terms[i].Term},
			Synonyms:    []string{},
		}
	}
	return clusters
}

// GenerateAltText asks Gemini to produce accessible alt text that narrates
// the trending topics and describes the bump chart for screen-reader users.
// Falls back to FormatAltText on any failure.
func (g *Grouper) GenerateAltText(ctx context.Context, ranked []IdentifiedTopic, trajectories map[string][]int) string {
	fallback := FormatAltText(ranked)

	if !g.checkAndIncrementRate() {
		return fallback
	}

	prompt := buildAltTextPrompt(ranked, trajectories)

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fallback
	}

	url := g.endpoint + "?key=" + g.apiKey
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return fallback
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("alt-text: API call failed: %v, using fallback", err)
		return fallback
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("alt-text: API returned %d: %s, using fallback", resp.StatusCode, string(respBody))
		return fallback
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fallback
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return fallback
	}

	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return fallback
	}

	alt := strings.TrimSpace(extractResponseText(gemResp.Candidates[0].Content.Parts))
	if alt == "" {
		return fallback
	}

	const maxAltLen = 1000
	if len(alt) > maxAltLen {
		alt = alt[:maxAltLen-3] + "..."
	}
	return alt
}

func buildAltTextPrompt(ranked []IdentifiedTopic, trajectories map[string][]int) string {
	var b strings.Builder
	b.WriteString("Write alt text for a social media image. The image contains:\n")
	b.WriteString("1. A bump chart showing how topic rankings changed over 24 hours\n")
	b.WriteString("2. The current top trending topics on Bluesky\n\n")
	b.WriteString("Current rankings:\n")

	for _, t := range ranked {
		line := fmt.Sprintf("- #%d: \"%s\" — %s (%d posts)", t.Rank, t.Cluster.Label, t.Cluster.Description, t.PostCount)
		if t.ExemplarHandle != "" {
			line += fmt.Sprintf(", exemplar by @%s", t.ExemplarHandle)
		}
		b.WriteString(line + "\n")
	}

	if len(trajectories) > 0 {
		b.WriteString("\nRank history (oldest→newest, 0 = not in top 5):\n")
		for _, t := range ranked {
			if ranks, ok := trajectories[t.TopicID]; ok {
				strs := make([]string, len(ranks))
				for i, r := range ranks {
					if r == 0 {
						strs[i] = "-"
					} else {
						strs[i] = fmt.Sprintf("#%d", r)
					}
				}
				fmt.Fprintf(&b, "- \"%s\": %s\n", t.Cluster.Label, strings.Join(strs, " → "))
			}
		}
	}

	b.WriteString("\nRequirements:\n")
	b.WriteString("- Write 2-4 sentences of plain English accessible alt text\n")
	b.WriteString("- First describe what people are talking about (the narrative)\n")
	b.WriteString("- Then briefly describe the bump chart visual (rising/falling lines, colors)\n")
	b.WriteString("- Do NOT use markdown, hashtags, or @mentions\n")
	b.WriteString("- Keep it under 900 characters\n")
	b.WriteString("- Return only the alt text, nothing else\n")
	return b.String()
}

func (g *Grouper) checkAndIncrementRate() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if now.Sub(g.lastReset) > 24*time.Hour {
		g.dailyCalls = 0
		g.lastReset = now
	}
	if g.dailyCalls >= maxDailyCalls {
		return false
	}
	g.dailyCalls++
	return true
}
