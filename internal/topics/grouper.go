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

	return clusters, nil
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
	b.WriteString("- \"label\": short human-readable topic name (2-4 words)\n")
	b.WriteString("- \"description\": one sentence describing the topic\n")
	b.WriteString("- \"keywords\": array of the original terms that belong to this group\n")
	b.WriteString("- \"synonyms\": array of additional related terms not in the original list\n")
	b.WriteString("\nRules:\n")
	b.WriteString("- Maximum 10 groups\n")
	b.WriteString("- Every input term must appear in exactly one group's keywords\n")
	b.WriteString("- Groups should be meaningful topics, not just word pairs\n")
	b.WriteString("- If a term doesn't fit any group, put it in its own single-term group\n")
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
