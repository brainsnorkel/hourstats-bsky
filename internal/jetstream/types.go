package jetstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	// maxDIDBytes bounds an event's DID. The AT Protocol caps a DID at 2048
	// characters, but every method on the network is far shorter; 256 bytes
	// admits did:plc and the did:web forms that actually appear while keeping a
	// hostile value out of the tombstone set, the bucket map and the URIs built
	// from it.
	maxDIDBytes = 256

	// maxRkeyBytes bounds a commit's record key. The lexicon caps an rkey at
	// 512 characters.
	maxRkeyBytes = 512
)

// validateEventIdentity rejects an event whose DID or record key is absent,
// not a DID at all, or longer than the protocol allows. Both are concatenated
// into AT URIs and used as map keys downstream, so an unbounded value is worth
// refusing at the decoder rather than carrying. rkey is "" for events that are
// not commits.
func validateEventIdentity(did, rkey string) error {
	switch {
	case did == "":
		return errors.New("jetstream: frame missing did")
	case !strings.HasPrefix(did, "did:"):
		return fmt.Errorf("jetstream: %q is not a did", boundDiag(did))
	case len(did) > maxDIDBytes:
		return fmt.Errorf("jetstream: did of %d bytes exceeds the %d-byte limit", len(did), maxDIDBytes)
	case len(rkey) > maxRkeyBytes:
		return fmt.Errorf("jetstream: rkey of %d bytes exceeds the %d-byte limit", len(rkey), maxRkeyBytes)
	}
	return nil
}

// rkeyOf is the event's record key, or "" when it carries no commit.
func (e *Event) rkeyOf() string {
	if e.Commit == nil {
		return ""
	}
	return e.Commit.Rkey
}

// Event is one Jetstream event, normalised across both wire protocols so the
// consumer's dispatch and the caller's handlers are protocol-agnostic.
// Jetstream events have 4 kinds: "commit", "identity", "account", "sync".
type Event struct {
	DID     string        `json:"did"`
	TimeUS  int64         `json:"time_us"`
	Kind    string        `json:"kind"`
	Commit  *Commit       `json:"commit,omitempty"`
	Account *AccountEvent `json:"account,omitempty"`

	// Seq is the v2 monotonic per-event sequence number, which is also the
	// v2 stream cursor. It is 0 on v1, whose frames carry no seq and whose
	// cursor is TimeUS.
	Seq int64 `json:"-"`
}

// AccountEvent is the payload of a "kind":"account" event: the PDS reporting
// that a repo's hosting status changed.
type AccountEvent struct {
	Active bool   `json:"active"`
	Status string `json:"status,omitempty"`
	DID    string `json:"did"`
	Time   string `json:"time,omitempty"`
}

// accountStillHostedStatuses are inactive statuses that do NOT mean the
// account's content is gone. Sync 1.1 added "desynchronized" and "throttled"
// as transient hosting states: the repo still exists and its posts must be
// kept.
var accountStillHostedStatuses = map[string]bool{
	"desynchronized": true,
	"throttled":      true,
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
	Type      string      `json:"$type"`
	Text      string      `json:"text"`
	CreatedAt string      `json:"createdAt"`
	Langs     []string    `json:"langs,omitempty"`
	Reply     *ReplyRef   `json:"reply,omitempty"`
	Labels    *SelfLabels `json:"labels,omitempty"`
}

type SelfLabels struct {
	Values []SelfLabelValue `json:"values,omitempty"`
}

type SelfLabelValue struct {
	Val string `json:"val"`
}

var adultLabelValues = map[string]bool{
	"porn": true, "sexual": true, "nudity": true, "graphic-media": true,
}

func (r *PostRecord) HasAdultContent() bool {
	if r.Labels == nil {
		return false
	}
	for _, v := range r.Labels.Values {
		if adultLabelValues[v.Val] {
			return true
		}
	}
	return false
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

// IsPostDelete returns true if this event deletes a post record. Delete
// commits carry no record and no cid.
func (e *Event) IsPostDelete() bool {
	return e.Kind == "commit" &&
		e.Commit != nil &&
		e.Commit.Operation == "delete" &&
		e.Commit.Collection == "app.bsky.feed.post"
}

// IsAccountInactive returns true if this event reports an account whose
// content should no longer be served — deactivated, deleted, suspended or
// takendown. Transient hosting states (desynchronized, throttled) leave the
// account in place and are not reported as inactive.
func (e *Event) IsAccountInactive() bool {
	return e.Kind == "account" &&
		e.Account != nil &&
		!e.Account.Active &&
		!accountStillHostedStatuses[e.Account.Status]
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
