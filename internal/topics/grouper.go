package topics

import (
	"context"
	"encoding/json"
	"errors"
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
	apiKey string
	// endpoint is the primary grouping model's generateContent URL.
	endpoint string
	// fallbackEndpoint is a cheaper model tried when the primary fails with a
	// retryable error (429/5xx/empty/unparseable). Empty disables the tier.
	fallbackEndpoint string
	httpClient       *http.Client
	mu               sync.Mutex
	dailyCalls       int
	lastReset        time.Time
	// budgetTripped records that the daily budget was hit in the current
	// window, so the warning and the stats event fire once per trip rather
	// than once per blocked call.
	budgetTripped     bool
	onBudgetExhausted func(dailyCalls int)
}

// groupingEndpoint builds the Gemini generateContent URL for a model name.
func groupingEndpoint(model string) string {
	return geminiBaseURL + model + ":generateContent"
}

// NewGrouper creates a Grouper for the given primary model. When fallbackModel
// is non-empty and differs from the primary, GroupAndLabel retries a failed
// primary call against it before giving up.
func NewGrouper(apiKey, model, fallbackModel string) *Grouper {
	if model == "" {
		model = DefaultGeminiModel
	}
	g := &Grouper{
		apiKey:   apiKey,
		endpoint: groupingEndpoint(model),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		lastReset: time.Now(),
	}
	if fallbackModel != "" && fallbackModel != model {
		g.fallbackEndpoint = groupingEndpoint(fallbackModel)
	}
	return g
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

// NewGrouperWithEndpoints creates a Grouper with custom primary and fallback
// endpoints (for testing the model-tier fallback path).
func NewGrouperWithEndpoints(apiKey, endpoint, fallbackEndpoint string) *Grouper {
	g := NewGrouperWithEndpoint(apiKey, endpoint)
	g.fallbackEndpoint = fallbackEndpoint
	return g
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

// retryableError marks a grouping failure that may succeed on a different
// model tier: transport errors, HTTP 429/5xx, or empty/unparseable output.
// Construction failures (marshal/request build) are deterministic and are
// returned unwrapped so they do not trigger a pointless fallback attempt.
type retryableError struct{ err error }

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

func retryablef(format string, a ...any) error {
	return &retryableError{fmt.Errorf(format, a...)}
}

func isRetryable(err error) bool {
	var r *retryableError
	return errors.As(err, &r)
}

// GroupAndLabel sends the top TF-IDF terms to Gemini and returns grouped
// clusters. It tries the primary model first; on a retryable failure it retries
// once against the fallback model (if configured). If every tier fails it
// returns an error so the caller suppresses the trending post rather than
// publishing degraded topics.
func (g *Grouper) GroupAndLabel(ctx context.Context, terms []TermScore) ([]TopicCluster, error) {
	if len(terms) == 0 {
		return nil, nil
	}

	if !g.checkAndIncrementRate() {
		return nil, fmt.Errorf("grouper: daily rate limit reached (limit %d)", maxDailyCalls)
	}

	headlines := FetchHeadlines(ctx)
	prompt := buildPrompt(terms, headlines)

	clusters, err := g.requestClusters(ctx, g.endpoint, prompt)
	if err != nil {
		if g.fallbackEndpoint == "" || !isRetryable(err) {
			return nil, err
		}
		slog.Warn("grouper: primary model failed, retrying with fallback model", "error", err)
		clusters, err = g.requestClusters(ctx, g.fallbackEndpoint, prompt)
		if err != nil {
			return nil, fmt.Errorf("grouper: fallback model also failed: %w", err)
		}
		slog.Info("grouper: served by fallback model")
	}

	for _, c := range clusters {
		slog.Info("grouper: topic", "label", c.Label, "justification", c.Justification)
	}

	if len(clusters) > MaxLLMGroups {
		clusters = clusters[:MaxLLMGroups]
	}

	clusters = normalizeClusterKeywords(clusters, terms)
	clusters = filterGenericClusters(clusters)
	return clusters, nil
}

// normalizeClusterKeywords lowercases and trims the model's keywords and drops
// any that were not among the terms it was given. A hallucinated or Title-Cased
// keyword can never match a post token, but it still inflates the exemplar
// relevance denominator, making genuine matches look less relevant than they
// are. Synonyms are invented by design, so they are only normalised, not
// filtered. Clusters left without a single known keyword are dropped.
func normalizeClusterKeywords(clusters []TopicCluster, terms []TermScore) []TopicCluster {
	allowed := make(map[string]bool, len(terms))
	for _, t := range terms {
		allowed[strings.ToLower(strings.TrimSpace(t.Term))] = true
	}

	out := make([]TopicCluster, 0, len(clusters))
	for _, c := range clusters {
		keywords := make([]string, 0, len(c.Keywords))
		seen := make(map[string]bool, len(c.Keywords))
		var dropped []string
		for _, k := range c.Keywords {
			k = strings.ToLower(strings.TrimSpace(k))
			if k == "" || seen[k] {
				continue
			}
			if !allowed[k] {
				dropped = append(dropped, k)
				continue
			}
			seen[k] = true
			keywords = append(keywords, k)
		}
		if len(dropped) > 0 {
			slog.Warn("grouper: dropping unknown keywords", "label", c.Label, "keywords", dropped)
		}
		if len(keywords) == 0 {
			slog.Warn("grouper: dropping cluster with no known keywords", "label", c.Label)
			continue
		}

		synonyms := make([]string, 0, len(c.Synonyms))
		for _, syn := range c.Synonyms {
			syn = strings.ToLower(strings.TrimSpace(syn))
			if syn == "" || seen[syn] {
				continue
			}
			seen[syn] = true
			synonyms = append(synonyms, syn)
		}

		c.Keywords = keywords
		c.Synonyms = synonyms
		out = append(out, c)
	}
	return out
}

// requestClusters performs one Gemini grouping call against the given endpoint
// and returns the parsed clusters (before post-processing). Failures that may
// succeed on a different model are wrapped via retryablef.
func (g *Grouper) requestClusters(ctx context.Context, endpoint, prompt string) ([]TopicCluster, error) {
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
		return nil, fmt.Errorf("grouper: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("grouper: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, retryablef("grouper: API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		statusErr := fmt.Errorf("grouper: API returned status %d: %s", resp.StatusCode, string(respBody))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, &retryableError{statusErr}
		}
		return nil, statusErr
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, retryablef("grouper: read response failed: %w", err)
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return nil, retryablef("grouper: unmarshal response failed: %w", err)
	}

	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return nil, retryablef("grouper: empty response from Gemini")
	}

	jsonText := extractResponseText(gemResp.Candidates[0].Content.Parts)
	if jsonText == "" {
		return nil, retryablef("grouper: no response text in Gemini output")
	}

	var clusters []TopicCluster
	if err := json.Unmarshal([]byte(jsonText), &clusters); err != nil {
		return nil, retryablef("grouper: parse clusters JSON failed: %w", err)
	}

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
		if strings.Trim(lower, "_ ") == "discard" {
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

func buildPrompt(terms []TermScore, headlines []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Group these %d TF-IDF terms from recent Bluesky posts into trending topics.\n\n", len(terms))
	b.WriteString("Terms (score):\n")
	for _, t := range terms {
		fmt.Fprintf(&b, "- %s (%.2f)\n", t.Term, t.Score)
	}

	if len(headlines) > 0 {
		b.WriteString("\nCURRENT NEWS HEADLINES (for disambiguation only):\n")
		for _, h := range headlines {
			fmt.Fprintf(&b, "- %s\n", h)
		}
		b.WriteString("Use these ONLY to pick the correct event name when TF-IDF terms are ambiguous.\n")
		b.WriteString("If terms don't match any headline, label based on the terms alone.\n")
		b.WriteString("Bluesky-specific topics (memes, community trends) will NOT appear in headlines — that is expected.\n\n")
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

// exemplarValidationResult is one verdict from the model. The id echoes the
// numbered pair in the prompt: several pairs can share a topic label now that
// each topic offers multiple candidates, so the label alone cannot identify
// which post was judged.
type exemplarValidationResult struct {
	ID         int  `json:"id"`
	IsRelevant bool `json:"is_relevant"`
}

var exemplarValidationSchema = map[string]interface{}{
	"type": "ARRAY",
	"items": map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"id":          map[string]interface{}{"type": "INTEGER"},
			"topic_label": map[string]interface{}{"type": "STRING"},
			"is_relevant": map[string]interface{}{"type": "BOOLEAN"},
		},
		"required": []string{"id", "topic_label", "is_relevant"},
	},
}

// ErrValidationUnavailable reports that a validation batch was not judged at
// all: the budget was spent, the API failed, or the response could not be
// aligned with the pairs that were sent. The pairs come back untouched, so the
// caller must not read their IsRelevant field as a verdict.
var ErrValidationUnavailable = errors.New("exemplar validation unavailable")

func (g *Grouper) ValidateExemplars(ctx context.Context, pairs []ExemplarValidation) ([]ExemplarValidation, error) {
	if len(pairs) == 0 {
		return pairs, nil
	}

	if !g.checkAndIncrementRate() {
		return pairs, fmt.Errorf("%w: daily call budget of %d reached", ErrValidationUnavailable, maxDailyCalls)
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
		return pairs, fmt.Errorf("%w: API call failed: %w", ErrValidationUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return pairs, fmt.Errorf("%w: API returned status %d: %s", ErrValidationUnavailable, resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return pairs, fmt.Errorf("%w: read response: %w", ErrValidationUnavailable, err)
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return pairs, fmt.Errorf("%w: unmarshal response: %w", ErrValidationUnavailable, err)
	}

	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return pairs, fmt.Errorf("%w: empty response", ErrValidationUnavailable)
	}

	jsonText := extractResponseText(gemResp.Candidates[0].Content.Parts)
	if jsonText == "" {
		return pairs, fmt.Errorf("%w: no response text", ErrValidationUnavailable)
	}

	var results []exemplarValidationResult
	if err := json.Unmarshal([]byte(jsonText), &results); err != nil {
		return pairs, fmt.Errorf("%w: parse JSON: %w", ErrValidationUnavailable, err)
	}

	verdicts, err := mapVerdicts(results, len(pairs))
	if err != nil {
		return pairs, err
	}
	for i := range pairs {
		pairs[i].IsRelevant = verdicts[i]
	}

	return pairs, nil
}

// mapVerdicts aligns the model's verdicts with the numbered pairs in the
// prompt. Ids are only trusted when they form the complete set 1..n, each once:
// a model that echoes 0-based ids would otherwise shift every verdict by one
// slot and leave the last pair at its default. When the ids are unusable but
// the model answered exactly once per pair, position is the next best evidence.
// Anything else means we do not know which post was judged, so the batch counts
// as unvalidated.
func mapVerdicts(results []exemplarValidationResult, n int) ([]bool, error) {
	if len(results) != n {
		return nil, fmt.Errorf("%w: got %d verdicts for %d pairs", ErrValidationUnavailable, len(results), n)
	}

	seen := make(map[int]bool, n)
	complete := true
	for _, r := range results {
		if r.ID < 1 || r.ID > n || seen[r.ID] {
			complete = false
			break
		}
		seen[r.ID] = true
	}

	out := make([]bool, n)
	if complete {
		for _, r := range results {
			out[r.ID-1] = r.IsRelevant
		}
		return out, nil
	}
	// The one id mistake we can read safely is a zero-based echo in order
	// (0..n-1). Anything else (scrambled, duplicated or missing ids) would
	// pin verdicts on the wrong posts if read positionally, so the batch is
	// reported as unvalidated and the caller keeps its top picks.
	zeroBased := true
	for i, r := range results {
		if r.ID != i {
			zeroBased = false
			break
		}
	}
	if !zeroBased {
		return nil, fmt.Errorf("%w: unusable verdict ids for %d pairs", ErrValidationUnavailable, n)
	}
	slog.Warn("validate-exemplars: zero-based verdict ids, reading in response order", "pairs", n)
	for i, r := range results {
		out[i] = r.IsRelevant
	}
	return out, nil
}

func buildValidationPrompt(pairs []ExemplarValidation) string {
	var b strings.Builder
	b.WriteString("For each numbered topic-post pair below, determine if the post is genuinely about the topic.\n")
	b.WriteString("A post is relevant if its main subject matches the topic. Tangential keyword overlap does NOT count.\n")
	b.WriteString("Several pairs may share a topic: judge each pair independently and echo its id.\n\n")

	for i, p := range pairs {
		fmt.Fprintf(&b, "%d. Topic: %q\n   Post: %q\n\n", i+1, p.TopicLabel, p.PostText)
	}

	b.WriteString("Return one entry per pair with its id, and is_relevant=true only if the post is genuinely about the topic, not just sharing a keyword.\n")
	return b.String()
}

// SetBudgetExhaustedHandler registers a callback invoked the first time the
// daily Gemini call budget is exhausted within a window. It runs outside the
// Grouper's lock, so it may block (e.g. to write a stats event).
func (g *Grouper) SetBudgetExhaustedHandler(fn func(dailyCalls int)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onBudgetExhausted = fn
}

// BudgetExhausted reports whether the daily call budget is currently spent.
func (g *Grouper) BudgetExhausted() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rollWindowLocked(time.Now())
	return g.budgetTripped
}

// rollWindowLocked clears the daily counters once the 24h window has elapsed.
func (g *Grouper) rollWindowLocked(now time.Time) {
	if now.Sub(g.lastReset) > 24*time.Hour {
		g.dailyCalls = 0
		g.budgetTripped = false
		g.lastReset = now
	}
}

// rateDecision is the outcome of one budget check, resolved under the lock so
// the logging and callback can run without holding it.
type rateDecision struct {
	allowed     bool
	firstTrip   bool
	dailyCalls  int
	windowStart time.Time
	handler     func(dailyCalls int)
}

func (g *Grouper) checkAndIncrementRate() bool {
	d := g.reserveCall(time.Now())
	if !d.allowed && d.firstTrip {
		slog.Warn("gemini: daily call budget exhausted",
			"daily_calls", d.dailyCalls, "max_daily_calls", maxDailyCalls,
			"window_started", d.windowStart.UTC().Format(time.RFC3339))
		if d.handler != nil {
			d.handler(d.dailyCalls)
		}
	}
	return d.allowed
}

// reserveCall takes one call off the daily budget if there is one left. All
// mutation happens here under a deferred unlock so no future early return can
// leak the lock.
func (g *Grouper) reserveCall(now time.Time) rateDecision {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rollWindowLocked(now)

	if g.dailyCalls >= maxDailyCalls {
		firstTrip := !g.budgetTripped
		g.budgetTripped = true
		return rateDecision{
			firstTrip:   firstTrip,
			dailyCalls:  g.dailyCalls,
			windowStart: g.lastReset,
			handler:     g.onBudgetExhausted,
		}
	}

	g.dailyCalls++
	return rateDecision{allowed: true, dailyCalls: g.dailyCalls, windowStart: g.lastReset}
}
