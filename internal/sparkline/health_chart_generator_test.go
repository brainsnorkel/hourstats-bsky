package sparkline

import (
	"bytes"
	"image/png"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

// healthSnapshots builds a short time series where RSS starts at rssMB and
// climbs. An rssMB of 0 simulates rows written before the rss_bytes column
// existed, which store 0 for every sample.
func healthSnapshots(n int, rssMB int64) []store.StatsSnapshot {
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	out := make([]store.StatsSnapshot, n)
	for i := range out {
		rss := int64(0)
		if rssMB > 0 {
			rss = (rssMB + int64(i)) * 1024 * 1024
		}
		out[i] = store.StatsSnapshot{
			SnapshotTime:      base.Add(time.Duration(i) * 30 * time.Minute),
			HeapInuseBytes:    int64(120+i) * 1024 * 1024,
			HeapSysBytes:      int64(200+i) * 1024 * 1024,
			SysBytes:          int64(380+i) * 1024 * 1024,
			RSSBytes:          rss,
			HeapReleasedBytes: int64(30+i) * 1024 * 1024,
			StackInuseBytes:   3 * 1024 * 1024,
			GCPauseTotalNs:    int64(5+i) * 1e6,
			GCCount:           int64(10 + i),
			GCCPUFraction:     0.015,
			WALSizeBytes:      int64(10+i) * 1024 * 1024,
			WriteChannelDepth: 50 + i,
			GoroutineCount:    25 + i,
			CycleDurationMs:   int64(40000 + i*100),
		}
	}
	return out
}

func decodeChart(t *testing.T, snaps []store.StatsSnapshot) {
	t.Helper()
	got, err := GenerateHealthChart(snaps, 1024)
	if err != nil {
		t.Fatalf("GenerateHealthChart: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != healthChartWidth || b.Dy() != healthChartHeight {
		t.Errorf("chart is %dx%d, want %dx%d", b.Dx(), b.Dy(), healthChartWidth, healthChartHeight)
	}
}

// TestGenerateHealthChart_WithRSS covers the normal path where RSS is
// recorded and becomes the primary memory series.
func TestGenerateHealthChart_WithRSS(t *testing.T) {
	decodeChart(t, healthSnapshots(8, 588))
}

// TestGenerateHealthChart_WithoutRSS covers the migration window and
// non-Linux hosts, where every rss_bytes is 0. The RSS series must be
// dropped rather than pinning the memory panel's floor to zero.
func TestGenerateHealthChart_WithoutRSS(t *testing.T) {
	decodeChart(t, healthSnapshots(8, 0))
}

// TestGenerateHealthChart_MixedRSS covers a restart across the migration:
// early rows have no RSS, later rows do. The series is kept, and the zero
// rows are honest gaps rather than a reason to drop real data.
func TestGenerateHealthChart_MixedRSS(t *testing.T) {
	snaps := healthSnapshots(8, 588)
	snaps[0].RSSBytes = 0
	snaps[1].RSSBytes = 0
	decodeChart(t, snaps)
}

func TestGenerateHealthChart_TooFewPoints(t *testing.T) {
	if _, err := GenerateHealthChart(healthSnapshots(1, 588), 1024); err == nil {
		t.Error("GenerateHealthChart with 1 point returned no error, want one")
	}
}
