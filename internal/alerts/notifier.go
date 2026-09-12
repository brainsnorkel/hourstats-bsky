package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
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
		now:        func() time.Time { return time.Now().UTC() },
	}
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
		// never conditional on the webhook. Every severity reaches Discord;
		// only actionable conditions and errors carry the mention.
		if c.Severity == SeverityInfo {
			slog.Info("alert", "name", c.Name, "severity", c.Severity, "message", c.Message)
		} else {
			slog.Warn("alert", "name", c.Name, "severity", c.Severity, "actionable", c.Actionable, "message", c.Message)
		}
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
// and the condition only: no DIDs, no handles, no post text, because the
// channel is not a place where the bot's input data belongs.
func (n *Notifier) postDiscord(ctx context.Context, c Condition) {
	if n.webhookURL == "" {
		return
	}

	mention := ""
	if c.Actionable || c.Severity == SeverityError {
		mention = n.mention
	}
	body, err := json.Marshal(discordPayload(mention, n.profile, c))
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
func discordPayload(mention, profile string, c Condition) map[string]any {
	content := discordMessage(profile, c)
	if mention != "" {
		content = mention + " " + content
	}
	return map[string]any{
		"content":          content,
		"allowed_mentions": map[string]any{"parse": []string{"users", "everyone"}},
	}
}

// discordMessage formats one condition for the channel.
func discordMessage(profile string, c Condition) string {
	tag := strings.ToUpper(c.Severity)
	if c.Actionable || c.Severity == SeverityError {
		tag += ", action needed"
	}
	return fmt.Sprintf("**hourstats-%s %s: %s**\n%s", profile, tag, c.Name, c.Message)
}
