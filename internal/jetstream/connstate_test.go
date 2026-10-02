package jetstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestConsumer_ConnectionStateRecordsDialFailure covers the stall detector's
// view: a refused dial leaves Connected=false with the error recorded, and the
// next successful connection clears it.
func TestConsumer_ConnectionStateRecordsDialFailure(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{subscribeSubprotocol}}
	var dials atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/"+subscribeNSID, func(w http.ResponseWriter, r *http.Request) {
		if dials.Add(1) == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	consumer := NewConsumer(ConsumerConfig{
		Endpoints:          []string{wsEndpoint(srv.URL)},
		DisableCompression: true,
		// A stored cursor puts ?cursor=... on the dial URL.
		LoadCursorV2: func(context.Context) (int64, int64, error) {
			return 100, time.Now().UnixMicro(), nil
		},
	})

	if st := consumer.ConnectionState(); st.Connected || st.LastError != "" {
		t.Fatalf("initial state = %+v, want disconnected with no error", st)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	var failed ConnectionState
	waitForSlow(t, func() bool {
		failed = consumer.ConnectionState()
		return failed.LastError != ""
	})
	if failed.Connected {
		t.Errorf("Connected = true after a refused dial")
	}
	if strings.Contains(failed.LastError, "?") {
		t.Errorf("LastError %q carries a query string", failed.LastError)
	}
	if failed.LastErrorAt.IsZero() {
		t.Errorf("LastErrorAt is zero")
	}
	if failed.Protocol != ProtocolV2 {
		t.Errorf("Protocol = %q, want %q", failed.Protocol, ProtocolV2)
	}
	if failed.Reconnects < 1 {
		t.Errorf("Reconnects = %d, want >= 1", failed.Reconnects)
	}
	if failed.Endpoint != wsEndpoint(srv.URL) {
		t.Errorf("Endpoint = %q, want %q", failed.Endpoint, wsEndpoint(srv.URL))
	}

	waitForSlow(t, func() bool {
		st := consumer.ConnectionState()
		return st.Connected && st.LastError == ""
	})
}

func TestSanitizeConnError(t *testing.T) {
	cases := []struct{ in, want string }{
		{"dial: websocket: bad handshake", "dial: websocket: bad handshake"},
		{`dial wss://host/xrpc/x?cursor=1759276800000000&collections=a: refused`, `dial wss://host/xrpc/x refused`},
		{`GET "wss://host/sub?cursor=1" failed`, `GET "wss://host/sub" failed`},
		{"trailing wss://host/sub?cursor=1", "trailing wss://host/sub"},
	}
	for _, tc := range cases {
		if got := sanitizeConnError(tc.in); got != tc.want {
			t.Errorf("sanitizeConnError(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := sanitizeConnError(strings.Repeat("é", 500))
	if n := len([]rune(long)); n != maxLastErrorRunes {
		t.Errorf("long message = %d runes, want %d", n, maxLastErrorRunes)
	}
}
