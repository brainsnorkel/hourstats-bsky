package jetstream

import "encoding/json"

// Event is the top-level Jetstream WebSocket message.
// Jetstream events have 3 kinds: "commit", "identity", "account".
type Event struct {
	DID    string  `json:"did"`
	TimeUS int64   `json:"time_us"`
	Kind   string  `json:"kind"`
	Commit *Commit `json:"commit,omitempty"`
}

// Commit represents a repo commit event (create/update/delete).
type Commit struct {
	Rev        string          `json:"rev"`
	Operation  string          `json:"operation"`
	Collection string          `json:"collection"`
	Rkey       string          `json:"rkey"`
	Record     json.RawMessage `json:"record,omitempty"`
	CID        string          `json:"cid,omitempty"`
}

// PostRecord is the parsed content of an app.bsky.feed.post record.
type PostRecord struct {
	Type      string    `json:"$type"`
	Text      string    `json:"text"`
	CreatedAt string    `json:"createdAt"`
	Langs     []string  `json:"langs,omitempty"`
	Reply     *ReplyRef `json:"reply,omitempty"`
}

// ReplyRef identifies the parent/root of a reply chain.
type ReplyRef struct {
	Parent *StrongRef `json:"parent,omitempty"`
	Root   *StrongRef `json:"root,omitempty"`
}

// StrongRef is an AT Protocol strong reference (URI + CID).
type StrongRef struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

// IsPostCreate returns true if this event is a new post creation.
func (e *Event) IsPostCreate() bool {
	return e.Kind == "commit" &&
		e.Commit != nil &&
		e.Commit.Operation == "create" &&
		e.Commit.Collection == "app.bsky.feed.post"
}

// PostURI constructs the AT Protocol URI for the post: at://<did>/app.bsky.feed.post/<rkey>
func (e *Event) PostURI() string {
	if e.Commit == nil {
		return ""
	}
	return "at://" + e.DID + "/app.bsky.feed.post/" + e.Commit.Rkey
}

// ParsePostRecord parses the commit record as a PostRecord.
// Returns nil if the record is empty or not parseable.
func (e *Event) ParsePostRecord() *PostRecord {
	if e.Commit == nil || len(e.Commit.Record) == 0 {
		return nil
	}
	var rec PostRecord
	if err := json.Unmarshal(e.Commit.Record, &rec); err != nil {
		return nil
	}
	return &rec
}
