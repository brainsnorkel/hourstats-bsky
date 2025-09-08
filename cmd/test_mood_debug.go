package main

import (
	"fmt"
	"math"
)

func getMoodWord100(netSentiment float64) string {
	sentiment := math.Max(-100, math.Min(100, netSentiment))
	normalizedSentiment := (sentiment + 100) / 200
	index := normalCurveMapping(normalizedSentiment)
	
	if index < 0 {
		index = 0
	} else if index >= len(sentimentWords100) {
		index = len(sentimentWords100) - 1
	}
	
	return sentimentWords100[index]
}

func normalCurveMapping(x float64) int {
	if x < 0.3 {
		return int(x * 100 * 1.2)
	} else if x > 0.7 {
		normalized := (x - 0.7) / 0.3
		return int(70 + normalized*29)
	} else {
		normalized := (x - 0.3) / 0.4
		power := 1.5
		compressed := math.Pow(normalized, power)
		return int(30 + compressed*39)
	}
}

var sentimentWords100 = []string{
	"hopeless", "devastated", "shattered", "destroyed", "ruined",
	"distressed", "anguished", "tormented", "crushed", "broken",
	"angry", "furious", "enraged", "livid", "outraged",
	"upset", "disturbed", "agitated", "perturbed", "unsettled",
	"frustrated", "exasperated", "annoyed", "irritated", "bothered",
	"disappointed", "let-down", "disheartened", "discouraged", "dejected",
	"worried", "anxious", "uneasy", "concerned", "troubled",
	"apprehensive", "nervous", "edgy", "tense", "restless",
	"cautious", "wary", "guarded", "reserved", "hesitant",
	"indifferent", "apathetic", "unmoved", "detached", "neutral",
	"calm", "peaceful", "serene", "tranquil", "composed",
	"pleased", "satisfied", "content", "gratified", "fulfilled",
	"cheerful", "upbeat", "bright", "sunny", "optimistic",
	"happy", "joyful", "glad", "delighted", "merry",
	"excited", "enthusiastic", "eager", "thrilled", "elated",
	"ecstatic", "overjoyed", "joyous", "rapturous", "exultant",
	"exhilarated", "exuberant", "vibrant", "energetic", "lively",
	"jubilant", "triumphant", "victorious", "celebratory", "festive",
	"transcendent", "blissful", "divine", "heavenly", "sublime",
	"euphoric", "magnificent", "glorious", "radiant", "exalted",
}

func main() {
	fmt.Println("Testing mood word mapping with debug info:")
	fmt.Printf("9.8%% -> %s (index: %d, normalized: %.3f)\n", 
		getMoodWord100(9.8), 
		normalCurveMapping((9.8+100)/200), 
		(9.8+100)/200)
	fmt.Printf("10.0%% -> %s (index: %d, normalized: %.3f)\n", 
		getMoodWord100(10.0), 
		normalCurveMapping((10.0+100)/200), 
		(10.0+100)/200)
	fmt.Printf("11.0%% -> %s (index: %d, normalized: %.3f)\n", 
		getMoodWord100(11.0), 
		normalCurveMapping((11.0+100)/200), 
		(11.0+100)/200)
}
