package formatter

import "math"

// getMoodWord100 maps sentiment percentage to one of 100 descriptive words
// providing much more nuanced emotional expression than the current 18-word system
func getMoodWord100(netSentiment float64) string {
	// Clamp sentiment to -100 to +100 range
	sentiment := math.Max(-100, math.Min(100, netSentiment))

	// Convert to 0-100 scale for array indexing
	// -100% becomes 0, 0% becomes 50, +100% becomes 100
	index := int((sentiment + 100) / 2)

	// Ensure index is within bounds
	if index < 0 {
		index = 0
	} else if index >= len(sentimentWords100) {
		index = len(sentimentWords100) - 1
	}

	return sentimentWords100[index]
}

// 100 carefully selected words representing the full emotional spectrum
// Each word represents approximately 2% of the sentiment range
var sentimentWords100 = []string{
	// -100% to -90%: Extreme negative (hopeless to devastated)
	"hopeless", "devastated", "shattered", "destroyed", "ruined",

	// -90% to -80%: Very high negative (distressed to crushed)
	"distressed", "anguished", "tormented", "crushed", "broken",

	// -80% to -70%: High negative (angry to outraged)
	"angry", "furious", "enraged", "livid", "outraged",

	// -70% to -60%: Strong negative (upset to unsettled)
	"upset", "disturbed", "agitated", "perturbed", "unsettled",

	// -60% to -50%: Moderate-high negative (frustrated to bothered)
	"frustrated", "exasperated", "annoyed", "irritated", "bothered",

	// -50% to -40%: Moderate negative (disappointed to dejected)
	"disappointed", "let-down", "disheartened", "discouraged", "dejected",

	// -40% to -30%: Mild-moderate negative (worried to troubled)
	"worried", "anxious", "uneasy", "concerned", "troubled",

	// -30% to -20%: Mild negative (apprehensive to tense)
	"apprehensive", "nervous", "edgy", "tense", "restless",

	// -20% to -10%: Slight negative (cautious to hesitant)
	"cautious", "wary", "guarded", "reserved", "hesitant",

	// -10% to 0%: Neutral-negative (indifferent to detached)
	"indifferent", "apathetic", "unmoved", "detached", "neutral",

	// 0% to +10%: Neutral-positive (calm to composed)
	"calm", "peaceful", "serene", "tranquil", "composed",

	// +10% to +20%: Slight positive (pleased to fulfilled)
	"pleased", "satisfied", "content", "gratified", "fulfilled",

	// +20% to +30%: Mild positive (cheerful to optimistic)
	"cheerful", "upbeat", "bright", "sunny", "optimistic",

	// +30% to +40%: Moderate positive (happy to delighted)
	"happy", "joyful", "glad", "delighted", "pleased",

	// +40% to +50%: Moderate-high positive (excited to elated)
	"excited", "enthusiastic", "eager", "thrilled", "elated",

	// +50% to +60%: High positive (ecstatic to rapturous)
	"ecstatic", "euphoric", "overjoyed", "blissful", "rapturous",

	// +60% to +70%: Very high positive (exhilarated to lively)
	"exhilarated", "exuberant", "vibrant", "energetic", "lively",

	// +70% to +80%: Extremely high positive (jubilant to festive)
	"jubilant", "triumphant", "victorious", "celebratory", "festive",

	// +80% to +90%: Near maximum positive (transcendent to sublime)
	"transcendent", "blissful", "divine", "heavenly", "sublime",

	// +90% to +100%: Maximum positive (euphoric to heavenly)
	"euphoric", "transcendent", "blissful", "divine", "heavenly",
}
