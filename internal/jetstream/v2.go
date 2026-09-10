package jetstream

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
)

// Protocol selectors for ConsumerConfig.Protocol.
const (
	// ProtocolV2 is the lexicon-framed wire protocol
	// (network.bsky.jetstream.subscribeEvents, xrpc.v1.json subprotocol,
	// seq cursors, optional dictionary zstd compression).
	ProtocolV2 = "v2"

	// ProtocolV1 is the legacy /subscribe endpoint: uncompressed text
	// frames, time_us cursors, wantedCollections filtering.
	ProtocolV1 = "v1"
)

const (
	// subscribeNSID is the lexicon the v2 tail dials at /xrpc/<nsid>.
	subscribeNSID = "network.bsky.jetstream.subscribeEvents"

	// getZstdDictionaryNSID serves the raw zstd dictionary over HTTPS.
	getZstdDictionaryNSID = "network.bsky.jetstream.getZstdDictionary"

	// subscribeSubprotocol is the xrpc.v1.json framing token offered via
	// Sec-WebSocket-Protocol. It is also the stream's lexicon default, so an
	// empty server echo means identical framing.
	subscribeSubprotocol = "xrpc.v1.json"

	// v2ReadLimit bounds one WebSocket message, and (as the decoder's memory
	// limit) one decompressed frame, so a hostile frame cannot force an
	// unbounded allocation.
	v2ReadLimit = 32 << 20

	// dictionaryFetchTimeout bounds the getZstdDictionary request.
	dictionaryFetchTimeout = 10 * time.Second

	// maxDictionaryBytes bounds the dictionary body we are willing to read.
	maxDictionaryBytes = 4 << 20

	// zstdDictMagic is the RFC 8878 §5 structured-dictionary magic number.
	zstdDictMagic = 0xEC30A437
)

// Payload $type values of network.bsky.jetstream.subscribeEvents#message.
const (
	v2TypeCommit   = subscribeNSID + "#commit"
	v2TypeIdentity = subscribeNSID + "#identity"
	v2TypeAccount  = subscribeNSID + "#account"
	v2TypeSync     = subscribeNSID + "#sync"
	v2TypeInfo     = subscribeNSID + "#info"
)

// v2Kinds are the event kinds we ask the server for. Post creates and deletes
// arrive as commits; account deactivations drive the author purge. Identity
// and sync events are of no use to the bot, so the server prunes them before
// they reach the socket.
var v2Kinds = []string{"commit", "account"}

// AllEndpointsV2 lists the public v2 subscribe endpoints, us-west first since
// the Fly.io app runs in sjc.
var AllEndpointsV2 = []string{
	"wss://jetstream.us-west.bsky.network/xrpc/" + subscribeNSID,
	"wss://jetstream.us-east.bsky.network/xrpc/" + subscribeNSID,
}

// Pre-upgrade dial refusals. The server rejects the handshake with HTTP 400
// and a JSON envelope {"error":...,"message":...} rather than an error frame.
var (
	// errCursorTooOld means the requested seq is below the server's retention
	// floor. Retrying the same cursor cannot succeed, so the caller discards
	// it and resumes from the live tip.
	errCursorTooOld = errors.New("jetstream: cursor too old")

	// errUnknownZstdDictionary means the server rotated its dictionary and no
	// longer serves the ID we pinned. Recoverable in place by refetching.
	errUnknownZstdDictionary = errors.New("jetstream: unknown zstd dictionary")

	// errInvalidRequest means the subscription parameters are unacceptable.
	// Reconnecting unchanged cannot succeed.
	errInvalidRequest = errors.New("jetstream: invalid subscribe request")
)

// errSkipFrame marks a well-formed frame that carries no caller-visible event:
// an #info advisory, or a message kind a newer server added.
var errSkipFrame = errors.New("jetstream: skip frame")

// v2Envelope is the xrpc.v1.json frame envelope: exactly one self-describing
// object per frame, discriminated by $type ("message" or "error").
type v2Envelope struct {
	Type    string          `json:"$type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
	Message string          `json:"message,omitempty"`
}

// v2Payload is the union of the subscribeEvents message refs. The kinds share
// the seq/did/time envelope; the rest of the fields are per-kind.
type v2Payload struct {
	Type string `json:"$type"`
	Seq  int64  `json:"seq"`
	DID  string `json:"did"`
	Time string `json:"time"`

	// #commit
	Rev        string          `json:"rev,omitempty"`
	Operation  string          `json:"operation,omitempty"`
	Collection string          `json:"collection,omitempty"`
	Rkey       string          `json:"rkey,omitempty"`
	Record     json.RawMessage `json:"record,omitempty"`
	CID        string          `json:"cid,omitempty"`

	// #account
	Account *AccountEvent `json:"account,omitempty"`

	// #info
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
}

// v2Info is an advisory #info frame (e.g. OutdatedCursor). It carries no seq
// and does not advance the cursor.
type v2Info struct {
	Name    string
	Message string
}

// v2StreamError is a terminal {"$type":"error"} frame. The server closes the
// connection immediately after sending one, so the caller reconnects.
type v2StreamError struct {
	Code    string
	Message string
}

func (e *v2StreamError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("jetstream: stream error: %s", e.Code)
	}
	return fmt.Sprintf("jetstream: stream error: %s: %s", e.Code, e.Message)
}

// maxDiagBytes bounds untrusted server-supplied diagnostic strings before they
// enter an error string or a log line.
const maxDiagBytes = 256

// boundDiag truncates s to at most maxDiagBytes, on a rune boundary.
func boundDiag(s string) string {
	if len(s) <= maxDiagBytes {
		return s
	}
	cut := maxDiagBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// decodeV2Frame parses one xrpc.v1.json frame into the protocol-agnostic
// Event. It returns errSkipFrame (with a non-nil info for #info advisories)
// for frames that carry no event, and a *v2StreamError for error frames.
func decodeV2Frame(data []byte) (*Event, *v2Info, error) {
	var env v2Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, nil, fmt.Errorf("decode v2 envelope: %w", err)
	}

	switch env.Type {
	case "message":
	case "error":
		if env.Error == "" {
			return nil, nil, errors.New("jetstream: error frame with no error code")
		}
		return nil, nil, &v2StreamError{Code: boundDiag(env.Error), Message: boundDiag(env.Message)}
	case "":
		// Not a newer revision but a malformed frame — a v1 endpoint dialled
		// as v2 would look healthy while delivering nothing.
		return nil, nil, errors.New("jetstream: frame missing envelope $type; is this a subscribeEvents endpoint?")
	default:
		// A newer protocol revision: skip rather than break.
		return nil, nil, errSkipFrame
	}

	if len(env.Payload) == 0 {
		return nil, nil, errors.New("jetstream: message frame missing payload")
	}

	var p v2Payload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return nil, nil, fmt.Errorf("decode v2 payload: %w", err)
	}

	if p.Type == v2TypeInfo {
		return nil, &v2Info{Name: boundDiag(p.Name), Message: boundDiag(p.Message)}, errSkipFrame
	}

	switch p.Type {
	case v2TypeCommit, v2TypeAccount, v2TypeIdentity, v2TypeSync:
	case "":
		return nil, nil, errors.New("jetstream: message payload missing $type")
	default:
		return nil, nil, errSkipFrame
	}

	// seq is 1-based on the wire; 0 means the required field was absent.
	if p.Seq <= 0 {
		return nil, nil, fmt.Errorf("jetstream: frame with invalid seq %d", p.Seq)
	}
	ts, err := time.Parse(time.RFC3339Nano, p.Time)
	if err != nil {
		return nil, nil, fmt.Errorf("jetstream: frame time %q: %w", boundDiag(p.Time), err)
	}
	if p.DID == "" {
		return nil, nil, errors.New("jetstream: frame missing did")
	}

	event := &Event{DID: p.DID, Seq: p.Seq, TimeUS: ts.UnixMicro()}

	switch p.Type {
	case v2TypeCommit:
		if p.Rev == "" || p.Collection == "" || p.Rkey == "" {
			return nil, nil, errors.New("jetstream: commit frame missing rev, collection or rkey")
		}
		event.Kind = "commit"
		event.Commit = &Commit{
			Rev:        p.Rev,
			Operation:  p.Operation,
			Collection: p.Collection,
			Rkey:       p.Rkey,
			Record:     p.Record,
			CID:        p.CID,
		}
	case v2TypeAccount:
		if p.Account == nil {
			return nil, nil, errors.New("jetstream: account frame missing account payload")
		}
		event.Kind = "account"
		event.Account = p.Account
		if event.Account.DID == "" {
			event.Account.DID = p.DID
		}
	case v2TypeIdentity:
		event.Kind = "identity"
	case v2TypeSync:
		event.Kind = "sync"
	}

	return event, nil, nil
}

// ---------------------------------------------------------------------------
// Dictionary zstd
// ---------------------------------------------------------------------------

// parseDictID extracts the dictionary ID from a structured zstd dictionary
// (RFC 8878 §5): the 4-byte little-endian magic 0xEC30A437 followed by the
// little-endian uint32 ID at bytes 4..8. ID 0 is reserved, and a raw
// content-only dictionary (no header) is not part of the wire contract.
func parseDictID(dict []byte) (uint32, error) {
	if len(dict) < 8 {
		return 0, fmt.Errorf("zstd dictionary: %d bytes is too short for a structured header", len(dict))
	}
	if binary.LittleEndian.Uint32(dict[:4]) != zstdDictMagic {
		return 0, errors.New("zstd dictionary: missing structured-dictionary magic")
	}
	id := binary.LittleEndian.Uint32(dict[4:8])
	if id == 0 {
		return 0, errors.New("zstd dictionary: ID 0 is reserved")
	}
	return id, nil
}

// dictionaryURL derives the getZstdDictionary URL from a wss:// subscribe
// endpoint on the same host.
func dictionaryURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint %q: %w", endpoint, err)
	}
	switch u.Scheme {
	case "wss", "https":
		u.Scheme = "https"
	case "ws", "http":
		u.Scheme = "http"
	default:
		return "", fmt.Errorf("unsupported endpoint scheme %q", u.Scheme)
	}
	u.Path = "/xrpc/" + getZstdDictionaryNSID
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// fetchDictionary downloads the server's current zstd dictionary and returns
// the blob with its parsed ID.
func fetchDictionary(ctx context.Context, endpoint string) ([]byte, uint32, error) {
	dictURL, err := dictionaryURL(endpoint)
	if err != nil {
		return nil, 0, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, dictionaryFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, dictURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build dictionary request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch dictionary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("fetch dictionary: unexpected status %d", resp.StatusCode)
	}
	blob, err := io.ReadAll(io.LimitReader(resp.Body, maxDictionaryBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("read dictionary: %w", err)
	}
	id, err := parseDictID(blob)
	if err != nil {
		return nil, 0, err
	}
	return blob, id, nil
}

// newZstdDecoder builds a dictionary-seeded decoder. The decoded-size cap
// mirrors the connection's read limit, so a hostile frame cannot expand toward
// the library's default ceiling.
func newZstdDecoder(dict []byte) (*zstd.Decoder, error) {
	dec, err := zstd.NewReader(nil,
		zstd.WithDecoderDicts(dict),
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(v2ReadLimit),
	)
	if err != nil {
		return nil, fmt.Errorf("build zstd decoder: %w", err)
	}
	return dec, nil
}

// classifyDialRefusal maps a pre-upgrade HTTP 400 body to a typed error. It
// returns nil when the response is not a recognised XRPC error envelope.
func classifyDialRefusal(resp *http.Response) error {
	if resp == nil || resp.StatusCode != http.StatusBadRequest || resp.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	var envelope struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	switch envelope.Error {
	case "CursorTooOld":
		return fmt.Errorf("%w: %s", errCursorTooOld, boundDiag(envelope.Message))
	case "UnknownZstdDictionary":
		return fmt.Errorf("%w: %s", errUnknownZstdDictionary, boundDiag(envelope.Message))
	case "InvalidRequest":
		return fmt.Errorf("%w: %s", errInvalidRequest, boundDiag(envelope.Message))
	}
	return nil
}
