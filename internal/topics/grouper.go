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
	defaultGeminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"
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
	Text string `json:"text"`
}

type geminiGenConfig struct {
	ResponseMimeType string `json:"responseMimeType"`
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

	jsonText := gemResp.Candidates[0].Content.Parts[0].Text

	var clusters []TopicCluster
	if err := json.Unmarshal([]byte(jsonText), &clusters); err != nil {
		log.Printf("grouper: parse clusters JSON: %v, using fallback", err)
		return fallbackClusters(terms), nil
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
}

func filterGenericClusters(clusters []TopicCluster) []TopicCluster {
	var filtered []TopicCluster
	for _, c := range clusters {
		words := strings.Fields(strings.ToLower(c.Label))
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

func buildPrompt(terms []TermScore) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Here are the top %d terms by TF-IDF score from recent social media posts.\n", len(terms))
	b.WriteString("Group synonyms and related terms together, then label each group.\n\n")
	b.WriteString("Terms (score):\n")
	for _, t := range terms {
		fmt.Fprintf(&b, "- %s (%.2f)\n", t.Term, t.Score)
	}
	b.WriteString("\nReturn a JSON array of groups. Each group has:\n")
	b.WriteString("- \"label\": short topic name (1-3 words, subject only — NO filler words like Posts, Mentions, Discussions, Event, Content, Media, Topics, News, Updates, Debate, Discourse, Controversy, Culture, Community, Platform, Social, Online)\n")
	b.WriteString("- \"description\": one sentence describing the topic\n")
	b.WriteString("- \"keywords\": array of the original terms that belong to this group\n")
	b.WriteString("- \"synonyms\": array of additional related terms not in the original list\n")
	b.WriteString("\nRules:\n")
	b.WriteString("- Maximum 10 groups\n")
	b.WriteString("- Every input term must appear in exactly one group's keywords\n")
	b.WriteString("- Groups should be meaningful, specific topics — not vague categories\n")
	b.WriteString("- NEVER create catch-all groups with labels like \"Miscellaneous\", \"General\", \"Various\", \"Other\", \"Everyday\", \"Actions\", \"Activities\", \"Mixed\", \"Uncategorized\", or \"Uncategorised\"\n")
	b.WriteString("- If a term doesn't fit a specific topic, leave it as a single-term group rather than lumping unrelated terms together\n")
	b.WriteString("- Merge sub-topics of the same event into one group (e.g. NFL jerseys + Super Bowl = one group, halftime show + Super Bowl = one group)\n")
	b.WriteString("- Terms containing underscores are multi-word phrases (e.g. bad_bunny means \"Bad Bunny\", super_bowl means \"Super Bowl\")\n")
	b.WriteString("- Prefer grouping underscore phrases with their component single-word terms\n")
	b.WriteString("- Use the multi-word form in labels when appropriate (e.g. label \"Bad Bunny\" not \"Bunny\")\n")
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

	alt := strings.TrimSpace(gemResp.Candidates[0].Content.Parts[0].Text)
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
