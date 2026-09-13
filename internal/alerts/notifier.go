package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

// suppressWindow is how long a condition stays quiet after it has been
// notified. Snapshots are taken every 30 minutes and most conditions latch for
// hours, so without this a single sustained problem would post every half hour
// until someone muted the channel.
const suppressWindow = time.Hour

// discordTimeout bounds the webhook POST. There are no retries: the log sink
// already has the alert, and a webhook that is down is not worth holding a
// goroutine for.
const discordTimeout = 10 * time.Second

// handleBudget bounds the whole handle resolution for one condition: five
// cold lookups are five PLC fetches, and an alert that is late is worse than
// an alert that names a DID.
const handleBudget = 5 * time.Second

// handleCacheTTL is how long a resolved handle is reused. An account that
// renames inside the hour is named by its old handle once; the DID in the
// profile link is right either way.
const handleCacheTTL = time.Hour

// handleCacheMax bounds the cache. Conditions are suppressed for an hour and
// name five accounts each, so this is far more than a day's worth; past it the
// expired entries go, and if they were all live the cache starts again.
const handleCacheMax = 256

// profileURLPrefix is the bsky.app profile link the alert ends with. The DID
// form works whatever the account renames itself to.
const profileURLPrefix = "https://bsky.app/profile/"

// handleEntry is one cached lookup. An empty handle is a remembered failure,
// so a DID that cannot be resolved is not looked up again every alert.
type handleEntry struct {
	handle string
	at     time.Time
}

// Notifier sends conditions to the log and, when configured, to a Discord
// webhook, at most once per condition name per suppressWindow.
type Notifier struct {
	profile    string
	webhookURL string
	// mention, when set, is prefixed to every Discord message: a user mention
	// in Discord's numeric form ("<@123456789012345678>"), "@here" or
	// "@everyone". Plain "@name" text does not ping anyone on Discord.
	mention    string
	httpClient *http.Client

	mu   sync.Mutex
	last map[string]time.Time

	// Handle resolution for the accounts a flood condition names. The
	// directory is built on first use rather than at construction: it holds a
	// 250k-entry cache, and most runs never fire one of these conditions.
	dirOnce  sync.Once
	dir      identity.Directory
	handleMu sync.Mutex
	handles  map[string]handleEntry

	// now is the clock, overridden in tests to step past suppressWindow
	// without sleeping.
	now func() time.Time
}

// NewNotifier builds a notifier for one app. webhookURL may be empty, in which
// case the log is the only sink; httpClient may be nil, in which case one with
// discordTimeout is built. The URL is a secret — it is stored here and never
// logged, not even on error.
func NewNotifier(profile, webhookURL string, httpClient *http.Client) *Notifier {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: discordTimeout}
	}
	return &Notifier{
		profile:    profile,
		webhookURL: webhookURL,
		httpClient: httpClient,
		last:       map[string]time.Time{},
		handles:    map[string]handleEntry{},
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// SetDirectory replaces the identity directory used to resolve DIDs to
// handles. It exists for tests and for a caller that already holds a directory
// worth sharing; the default is identity.DefaultDirectory().
func (n *Notifier) SetDirectory(d identity.Directory) {
	if n == nil {
		return
	}
	n.dirOnce.Do(func() {})
	n.dir = d
}

// directory returns the identity directory, building the default one the first
// time an alert actually needs it.
func (n *Notifier) directory() identity.Directory {
	n.dirOnce.Do(func() {
		if n.dir == nil {
			n.dir = identity.DefaultDirectory()
		}
	})
	return n.dir
}

// Notify reports every condition that is not currently suppressed.
func (n *Notifier) Notify(ctx context.Context, conds []Condition) {
	if n == nil {
		return
	}
	for _, c := range conds {
		if !n.claim(c.Name) {
			continue
		}
		// The log is the sink that is always there, so it goes first and is
		// never conditional on the webhook. Info stays in the log and on
		// /stats/health; warnings and errors reach Discord, and only
		// actionable conditions and errors carry the mention.
		if c.Severity == SeverityInfo {
			slog.Info("alert", "name", c.Name, "severity", c.Severity, "message", logMessage(c))
			logAccounts(c)
			continue
		}
		slog.Warn("alert", "name", c.Name, "severity", c.Severity, "actionable", c.Actionable, "message", logMessage(c))
		logAccounts(c)
		n.postDiscord(ctx, c)
	}
}

// claim reports whether name may be notified now, recording the notification
// if so. The map is keyed on the name rather than the message so a condition
// whose numbers change every window is still one alert.
func (n *Notifier) claim(name string) bool {
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if last, ok := n.last[name]; ok && now.Sub(last) < suppressWindow {
		return false
	}
	n.last[name] = now
	return true
}

// SetMention sets the text prefixed to every Discord message so a person or
// the channel is pinged. Discord only pings numeric user mentions
// ("<@id>"), "@here" and "@everyone".
func (n *Notifier) SetMention(m string) {
	if n == nil {
		return
	}
	n.mention = strings.TrimSpace(m)
}

// postDiscord sends one condition to the webhook. The body carries the profile
// and the condition, plus — for the conditions that name accounts — those
// accounts' handles and a profile link: the channel is the operator's own, and
// naming the account is the whole point of a flood alert. No post text is ever
// sent.
func (n *Notifier) postDiscord(ctx context.Context, c Condition) {
	if n.webhookURL == "" {
		return
	}

	mention := ""
	if c.Actionable || c.Severity == SeverityError {
		mention = n.mention
	}
	body, err := json.Marshal(discordPayload(mention, n.profile, c, n.resolveHandles(ctx, c.Accounts)))
	if err != nil {
		slog.Warn("alert webhook payload could not be encoded", "name", c.Name, "error", err)
		return
	}

	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discordTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		// err would embed the URL, so only the condition is logged.
		slog.Warn("alert webhook request could not be built", "name", c.Name)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		// A *url.Error prints the URL it failed on, which is the secret, so
		// the error is reduced to its presence.
		slog.Warn("alert webhook post failed", "name", c.Name)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.Warn("alert webhook returned an error status", "name", c.Name, "status", resp.StatusCode)
	}
}

// discordPayload is the webhook body. allowed_mentions must name the mention
// kinds explicitly or Discord renders "<@id>" and "@here" as inert text.
func discordPayload(mention, profile string, c Condition, names map[string]string) map[string]any {
	content := discordMessage(profile, c, names)
	if mention != "" {
		content = mention + " " + content
	}
	return map[string]any{
		"content":          content,
		"allowed_mentions": map[string]any{"parse": []string{"users", "everyone"}},
	}
}

// discordMessage formats one condition for the channel. names maps a DID to
// the handle to show in its place — the message is written with DIDs so the
// process log and /stats/health carry the stable identifier, and only the
// channel sees the friendly one. The busiest account's profile link is the
// last line, so the operator can go and look.
func discordMessage(profile string, c Condition, names map[string]string) string {
	tag := strings.ToUpper(c.Severity)
	if c.Actionable || c.Severity == SeverityError {
		tag += ", action needed"
	}
	message := c.Message
	for did, handle := range names {
		message = strings.ReplaceAll(message, did, handle)
	}
	if len(c.Accounts) > 0 && c.Accounts[0].DID != "" {
		message += "\n" + profileURLPrefix + c.Accounts[0].DID
	}
	return fmt.Sprintf("**hourstats-%s %s: %s**\n%s", profile, tag, c.Name, message)
}

// logMessage is the message with the per-account breakdown cut off. The
// accounts name individual people: they belong in the operator's channel and
// in a Debug line, not in the process log at Info or above.
func logMessage(c Condition) string {
	if len(c.Accounts) == 0 {
		return c.Message
	}
	if idx := strings.Index(c.Message, accountsPrefix); idx >= 0 {
		return strings.TrimSpace(c.Message[:idx])
	}
	return c.Message
}

// logAccounts records the breakdown at Debug, where DIDs are allowed.
func logAccounts(c Condition) {
	if len(c.Accounts) == 0 {
		return
	}
	pairs := make([]string, 0, len(c.Accounts))
	for _, a := range c.Accounts {
		pairs = append(pairs, a.DID+"="+strconv.FormatInt(a.Count, 10))
	}
	slog.Debug("alert top accounts", "name", c.Name, "accounts", pairs)
}

// resolveHandles maps as many of the accounts' DIDs to handles as it can
// inside one budget, caching what it learns (including the failures) for an
// hour. A DID that cannot be resolved is simply absent, which leaves the DID
// itself in the message.
func (n *Notifier) resolveHandles(ctx context.Context, accounts []DIDCount) map[string]string {
	if len(accounts) == 0 {
		return nil
	}
	names := make(map[string]string, len(accounts))
	pending := make([]string, 0, len(accounts))
	for _, a := range accounts {
		if a.DID == "" {
			continue
		}
		if handle, cached := n.cachedHandle(a.DID); cached {
			if handle != "" {
				names[a.DID] = handle
			}
			continue
		}
		pending = append(pending, a.DID)
	}
	if len(pending) == 0 {
		// A warm cache does no network work and needs no budget.
		return names
	}

	// One budget for every lookup this condition needs, so five cold DIDs
	// cannot cost five timeouts.
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handleBudget)
	defer cancel()
	for _, did := range pending {
		handle := n.lookupHandle(lookupCtx, did)
		n.cacheHandle(did, handle)
		if handle != "" {
			names[did] = handle
		}
	}
	return names
}

// lookupHandle resolves one DID, returning "" for anything that does not come
// back as a verified handle.
func (n *Notifier) lookupHandle(ctx context.Context, did string) string {
	parsed, err := syntax.ParseDID(did)
	if err != nil {
		return ""
	}
	ident, err := n.directory().LookupDID(ctx, parsed)
	if err != nil || ident == nil {
		slog.Debug("alert could not resolve a did to a handle", "did", did, "error", err)
		return ""
	}
	if ident.Handle.IsInvalidHandle() {
		return ""
	}
	return ident.Handle.String()
}

// cachedHandle returns a cached lookup and whether there was one. An empty
// handle with cached true is a remembered failure.
func (n *Notifier) cachedHandle(did string) (string, bool) {
	n.handleMu.Lock()
	defer n.handleMu.Unlock()
	entry, ok := n.handles[did]
	if !ok || n.now().Sub(entry.at) >= handleCacheTTL {
		return "", false
	}
	return entry.handle, true
}

// cacheHandle records a lookup, dropping the expired entries (and, failing
// that, everything) when the cache reaches its bound.
func (n *Notifier) cacheHandle(did, handle string) {
	now := n.now()
	n.handleMu.Lock()
	defer n.handleMu.Unlock()
	if n.handles == nil {
		n.handles = map[string]handleEntry{}
	}
	if len(n.handles) >= handleCacheMax {
		for key, entry := range n.handles {
			if now.Sub(entry.at) >= handleCacheTTL {
				delete(n.handles, key)
			}
		}
		if len(n.handles) >= handleCacheMax {
			n.handles = map[string]handleEntry{}
		}
	}
	n.handles[did] = handleEntry{handle: handle, at: now}
}
