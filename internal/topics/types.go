package topics

import "time"

const (
	MinDocFrequency   = 10
	MinUniqueAuthors  = 3
	MinCorpusSize     = 100
	MaxTFIDFTerms     = 50
	MaxLLMGroups      = 10
	TopTopics         = 5
	MaxTermFreqPerDoc = 3
)

type TermScore struct {
	Term    string
	Score   float64
	DocFreq int
}

type TopicCluster struct {
	Label         string   `json:"label"`
	Description   string   `json:"description"`
	Keywords      []string `json:"keywords"`
	Synonyms      []string `json:"synonyms"`
	Justification string   `json:"justification,omitempty"`
	IsMeme        bool     `json:"is_meme"`
}

type RankedTopic struct {
	Cluster           TopicCluster
	UniqueAuthorCount int
}

type IdentifiedTopic struct {
	RankedTopic
	TopicID        string
	Rank           int
	ExemplarURI    string
	ExemplarHandle string
}

// SentimentExtreme is one end of the seven-day sentiment range: the hour that
// hit it and the trending topic recorded for that hour.
type SentimentExtreme struct {
	Value float64   // net sentiment, in percent
	At    time.Time // the hour the reading belongs to
	Topic string    // rank-1 topic that hour, "" when none was recorded
}

// WeekExtremes carries the week's highest and lowest hour so the trending post
// can close with them.
type WeekExtremes struct {
	High SentimentExtreme
	Low  SentimentExtreme
}
