package jetstream

import (
	"bytes"
	"context"
	"log/slog"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/denylist"
)

// DefaultDenyListReload is how often the stored denylist is re-read, so a DID
// added over `fly ssh` takes effect without a deploy.
const DefaultDenyListReload = 5 * time.Minute

// createOperation is the exact commit operation token a denied frame must
// carry: only creates are refused, so a delete or an account event from a
// denied repo still reaches the caller and still removes its posts.
var createOperation = []byte(`"operation":"create"`)

// refreshDenyList republishes the union of the configured DenyDIDs and the
// stored list. A failed load leaves the previous set in place rather than
// silently emptying it.
func (c *Consumer) refreshDenyList(ctx context.Context) {
	union := make([]string, 0, len(c.cfg.DenyDIDs))
	union = append(union, c.cfg.DenyDIDs...)

	if c.cfg.LoadDenyList != nil {
		stored, err := c.cfg.LoadDenyList(ctx)
		if err != nil {
			slog.Warn("failed to load the stored firehose denylist, keeping the current one",
				"error", err, "denied_dids", denylist.Len())
			return
		}
		union = append(union, stored...)
	}

	before := denylist.Len()
	denylist.Replace(union)
	after := denylist.Len()
	if after != before {
		// The DIDs themselves are only ever logged at Debug: the list names
		// individual accounts and belongs in the operator's own records.
		slog.Info("firehose denylist updated", "denied_dids", after, "previous", before)
		slog.Debug("firehose denylist contents", "dids", union)
	}
}

// denyListReloadLoop re-reads the stored denylist on an interval. It is only
// started when a loader is configured; the environment half of the union never
// changes within a process.
func (c *Consumer) denyListReloadLoop(ctx context.Context) {
	interval := c.cfg.DenyListReload
	if interval <= 0 {
		interval = DefaultDenyListReload
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refreshDenyList(ctx)
		}
	}
}

// deniedCreateDID returns the denied repo a raw frame's create belongs to, or
// "" when the frame is not a denied create. It is a bounded byte scan, so a
// denied frame is dropped before the language pre-filter and is therefore
// never parsed and never counted as a post of its language. It costs nothing
// while the denylist is empty, which is the norm.
func deniedCreateDID(data []byte) string {
	if denylist.Len() == 0 {
		return ""
	}
	if !bytes.Contains(data, createOperation) {
		return ""
	}
	// In both wire protocols the repo's DID precedes the record: v1 carries it
	// at the top level, v2 on the payload ahead of the commit fields.
	did := scanFrameDID(data)
	if !denylist.Contains(did) {
		return ""
	}
	return did
}

// countDeniedCreate counts one dropped create from a denied repo. The DID only
// ever reaches a Debug line, never an Info one.
func (c *Consumer) countDeniedCreate(did string) {
	c.stats.PostsDenied.Add(1)
	c.offenders.addDenied(did)
	slog.Debug("dropped a create from a denied did", "did", did)
}
