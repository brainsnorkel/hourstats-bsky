package main

import (
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/jetstream"
)

func TestStallEventDetails(t *testing.T) {
	tests := []struct {
		name       string
		age        time.Duration
		state      jetstream.ConnectionState
		forced     bool
		reconnects int64
		want       string
	}{
		{
			name: "consumer stuck in its dial loop",
			age:  10*time.Minute + 30*time.Second + 400*time.Millisecond,
			state: jetstream.ConnectionState{
				Endpoint:     "wss://jetstream1.us-east.bsky.network/xrpc/network.bsky.jetstream.subscribeEvents",
				Protocol:     "v2",
				LastError:    "dial: dial tcp 1.2.3.4:443: i/o timeout",
				LastErrorAge: 4*time.Second + 900*time.Millisecond,
			},
			reconnects: 7,
			want:       `last_post_age=10m30s connected=false forced_reconnect=false reconnects_since_last_check=7 endpoint=jetstream1.us-east.bsky.network protocol=v2 last_error="dial: dial tcp 1.2.3.4:443: i/o timeout" last_error_age=4s`,
		},
		{
			name: "silent connection forced closed",
			age:  6 * time.Minute,
			state: jetstream.ConnectionState{
				Connected: true,
				Endpoint:  "wss://jetstream2.us-west.bsky.network/subscribe",
				Protocol:  "v1",
			},
			forced: true,
			want:   `last_post_age=6m0s connected=true forced_reconnect=true reconnects_since_last_check=0 endpoint=jetstream2.us-west.bsky.network protocol=v1 last_error="none" last_error_age=-`,
		},
		{
			name: "no consumer",
			age:  5*time.Minute + time.Second,
			want: `last_post_age=5m1s connected=false forced_reconnect=false reconnects_since_last_check=0 endpoint=- protocol=- last_error="none" last_error_age=-`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stallEventDetails(tt.age, tt.state, tt.forced, tt.reconnects); got != tt.want {
				t.Errorf("stallEventDetails =\n  %s\nwant\n  %s", got, tt.want)
			}
		})
	}
}

func TestConsumerHandleStateWithoutConsumer(t *testing.T) {
	var h consumerHandle
	if _, ok := h.state(); ok {
		t.Error("state() ok = true with no consumer set")
	}
	h.set(jetstream.NewConsumer(jetstream.ConsumerConfig{Endpoint: "wss://example.test/subscribe"}))
	st, ok := h.state()
	if !ok || st.Connected || st.Endpoint != "wss://example.test/subscribe" {
		t.Errorf("state() = %+v, %t; want a disconnected state for the set endpoint", st, ok)
	}
}
