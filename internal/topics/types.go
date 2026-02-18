package topics

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
