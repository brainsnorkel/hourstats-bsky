package topics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	geminiBaseURL      = "https://generativelanguage.googleapis.com/v1beta/models/"
	DefaultGeminiModel = "gemini-2.5-pro"
	maxDailyCalls      = 150
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

func NewGrouper(apiKey, model string) *Grouper {
	if model == "" {
		model = DefaultGeminiModel
	}
	return &Grouper{
		apiKey:   apiKey,
		endpoint: geminiBaseURL + model + ":generateContent",
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
			"is_meme":       map[string]interface{}{"type": "BOOLEAN"},
		},
		"required": []string{"label", "description", "keywords", "synonyms", "justification", "is_meme"},
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
		slog.Warn("grouper: daily rate limit reached, using fallback", "limit", maxDailyCalls)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fallbackClusters(terms), fmt.Errorf("grouper: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		slog.Warn("grouper: API call failed, using fallback", "error", err)
		return fallbackClusters(terms), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Warn("grouper: API returned non-OK, using fallback", "status", resp.StatusCode, "body", string(respBody))
		return fallbackClusters(terms), nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("grouper: read response failed, using fallback", "error", err)
		return fallbackClusters(terms), nil
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		slog.Warn("grouper: unmarshal response failed, using fallback", "error", err)
		return fallbackClusters(terms), nil
	}

	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		slog.Warn("grouper: empty response from Gemini, using fallback")
		return fallbackClusters(terms), nil
	}

	jsonText := extractResponseText(gemResp.Candidates[0].Content.Parts)
	if jsonText == "" {
		slog.Warn("grouper: no response text in Gemini output, using fallback")
		return fallbackClusters(terms), nil
	}

	var clusters []TopicCluster
	if err := json.Unmarshal([]byte(jsonText), &clusters); err != nil {
		slog.Warn("grouper: parse clusters JSON failed, using fallback", "error", err)
		return fallbackClusters(terms), nil
	}

	for _, c := range clusters {
		slog.Info("grouper: topic", "label", c.Label, "justification", c.Justification)
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
	b.WriteString("DISAMBIGUATION:\n")
	b.WriteString("- A person's surname may coincide with a famous place, event, or object (e.g. \"bondi\" = Pam Bondi OR Bondi Beach; \"bush\" = George Bush OR the Australian bush).\n")
	b.WriteString("- When a bare word is ambiguous, use co-occurring compound terms to determine the correct sense. For example, if \"bondi\" appears alongside \"pam_bondi\" or \"attorney_general\", it refers to the PERSON, not the beach.\n")
	b.WriteString("- NEVER create a place/event/object topic from an ambiguous surname unless place-specific terms (e.g. \"beach\", \"surf\", \"sydney\") also appear in the input.\n")
	b.WriteString("- When the dominant sense is a person, merge the bare surname into that person's topic and do NOT create a separate place topic.\n\n")
	b.WriteString("DISCARD:\n")
	b.WriteString("- Group vague, generic, or meta-commentary terms under the label \"__discard__\".\n")
	b.WriteString("- This includes: reaction words, mood words, generic adjectives, medium/format descriptions, and anything that describes HOW people are talking rather than WHAT they are talking about.\n")
	b.WriteString("- It is better to discard borderline terms than to create a weak topic.\n\n")
	b.WriteString("MEME DETECTION:\n")
	b.WriteString("- Set is_meme to true when the topic is a viral phrase or meme format that many users are repeating, rather than a news event, person, or subject.\n")
	b.WriteString("- Meme topics are catchphrases, copypastas, joke formats, or viral phrases — the content IS the repeated text itself.\n")
	b.WriteString("- Non-meme topics are events, people, organisations, products — things people are TALKING ABOUT.\n\n")
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
			IsMeme:      false,
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fallback
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		slog.Warn("alt-text: API call failed, using fallback", "error", err)
		return fallback
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Warn("alt-text: API returned non-OK, using fallback", "status", resp.StatusCode, "body", string(respBody))
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
		line := fmt.Sprintf("- #%d: \"%s\" — %s (%d authors)", t.Rank, t.Cluster.Label, t.Cluster.Description, t.UniqueAuthorCount)
		if t.Cluster.IsMeme {
			line += " (viral meme/phrase — search link provided)"
		} else if t.ExemplarHandle != "" {
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

type ExemplarValidation struct {
	TopicLabel string `json:"topic_label"`
	PostText   string `json:"post_text"`
	IsRelevant bool   `json:"is_relevant"`
}

var exemplarValidationSchema = map[string]interface{}{
	"type": "ARRAY",
	"items": map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"topic_label": map[string]interface{}{"type": "STRING"},
			"is_relevant": map[string]interface{}{"type": "BOOLEAN"},
		},
		"required": []string{"topic_label", "is_relevant"},
	},
}

func (g *Grouper) ValidateExemplars(ctx context.Context, pairs []ExemplarValidation) ([]ExemplarValidation, error) {
	if len(pairs) == 0 {
		return pairs, nil
	}

	if !g.checkAndIncrementRate() {
		slog.Warn("validate-exemplars: rate limit reached, skipping validation")
		return pairs, nil
	}

	prompt := buildValidationPrompt(pairs)

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
		},
		GenerationConfig: geminiGenConfig{
			ResponseMimeType: "application/json",
			ResponseSchema:   exemplarValidationSchema,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return pairs, fmt.Errorf("validate-exemplars: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, strings.NewReader(string(body)))
	if err != nil {
		return pairs, fmt.Errorf("validate-exemplars: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		slog.Warn("validate-exemplars: API call failed, skipping", "error", err)
		return pairs, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Warn("validate-exemplars: API returned non-OK, skipping", "status", resp.StatusCode, "body", string(respBody))
		return pairs, nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return pairs, nil
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return pairs, nil
	}

	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return pairs, nil
	}

	jsonText := extractResponseText(gemResp.Candidates[0].Content.Parts)
	if jsonText == "" {
		return pairs, nil
	}

	var results []ExemplarValidation
	if err := json.Unmarshal([]byte(jsonText), &results); err != nil {
		slog.Warn("validate-exemplars: parse JSON failed, skipping", "error", err)
		return pairs, nil
	}

	resultMap := make(map[string]bool)
	for _, r := range results {
		resultMap[r.TopicLabel] = r.IsRelevant
	}

	for i := range pairs {
		if relevant, ok := resultMap[pairs[i].TopicLabel]; ok {
			pairs[i].IsRelevant = relevant
		}
	}

	return pairs, nil
}

func buildValidationPrompt(pairs []ExemplarValidation) string {
	var b strings.Builder
	b.WriteString("For each topic-post pair below, determine if the post is genuinely about the topic.\n")
	b.WriteString("A post is relevant if its main subject matches the topic. Tangential keyword overlap does NOT count.\n\n")

	for i, p := range pairs {
		fmt.Fprintf(&b, "%d. Topic: %q\n   Post: %q\n\n", i+1, p.TopicLabel, p.PostText)
	}

	b.WriteString("Return is_relevant=true only if the post is genuinely about the topic, not just sharing a keyword.\n")
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
