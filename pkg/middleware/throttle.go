package middleware

// Ported from go-chi middleware, source:
// https://github.com/go-chi/chi/blob/master/middleware/throttle.go
// Added websocket support and metrics

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/monitoring"
)

const (
	errCapacityExceeded = "server capacity exceeded"
	errTimedOut         = "timed out while waiting for a pending request to complete"
	errContextCanceled  = "context was canceled"
)

var (
	defaultBacklogTimeout = time.Second * 60
)

// ThrottleOpts represents a set of throttling options.
type ThrottleOpts struct {
	Limit          int
	BacklogLimit   int
	Context        context.Context
	BacklogTimeout time.Duration
	Observer       monitoring.Observer
}

// token represents a request that is being processed.
type token struct{}

// throttler limits number of currently processed requests at a time.
type throttler struct {
	tokens         chan token
	backlogTokens  chan token
	backlogTimeout time.Duration
	mon            monitoring.Observer
}

func (t *throttler) GetMonitoringEventTypes() []string {
	return []string{monitoring.MetricProxyThortleBacklogSize}
}

func (t *throttler) SetObserver(mon monitoring.Observer) {
	t.mon = mon
}

func ThrottleWithOpts(opts ThrottleOpts) func(http.Handler) http.Handler {
	if opts.Limit < 1 {
		panic("Throttle expects limit > 0")
	}

	if opts.BacklogLimit < 0 {
		panic("Throttle expects backlogLimit to be positive")
	}

	if opts.BacklogTimeout.Seconds() == 0 {
		opts.BacklogTimeout = defaultBacklogTimeout
	}

	t := throttler{
		tokens:         make(chan token, opts.Limit),
		backlogTokens:  make(chan token, opts.Limit+opts.BacklogLimit),
		backlogTimeout: opts.BacklogTimeout,
	}

	if opts.Observer != nil {
		opts.Observer.Register(&t)
	}

	// Filling tokens.
	for i := 0; i < opts.Limit+opts.BacklogLimit; i++ {
		if i < opts.Limit {
			t.tokens <- token{}
		}
		t.backlogTokens <- token{}
	}

	go func() {
		ticker := time.NewTicker(15 * time.Second)
		for range ticker.C {
			t.mon.ProcessEvent(monitoring.MetricProxyThortleBacklogSize, float64(len(t.backlogTokens)), "Backlog")
			t.mon.ProcessEvent(monitoring.MetricProxyThortleBacklogSize, float64(len(t.tokens)), "Limit")
		}
	}()

	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			select {

			case <-ctx.Done():
				if IsWebsocket(r) {
					CancelConnection(ctx, errors.New(errContextCanceled))
				} else {
					http.Error(w, errContextCanceled, http.StatusTooManyRequests)
				}
				return

			case btok := <-t.backlogTokens:
				defer func() {
					t.backlogTokens <- btok
				}()
				timer := time.NewTimer(t.backlogTimeout)

				select {
				case <-timer.C:
					if IsWebsocket(r) {
						CancelConnection(ctx, errors.New(errCapacityExceeded))
					} else {
						http.Error(w, errTimedOut, http.StatusTooManyRequests)
					}
					return
				case <-ctx.Done():
					timer.Stop()
					if IsWebsocket(r) {
						CancelConnection(ctx, errors.New(errCapacityExceeded))
					} else {
						http.Error(w, errContextCanceled, http.StatusTooManyRequests)
					}
					return
				case tok := <-t.tokens:
					defer func() {
						timer.Stop()
						t.tokens <- tok
					}()
					next.ServeHTTP(w, r)
				}
				return

			default:
				if IsWebsocket(r) {
					CancelConnection(ctx, errors.New(errCapacityExceeded))
				} else {
					http.Error(w, errCapacityExceeded, http.StatusTooManyRequests)
				}
				return
			}
		}

		return http.HandlerFunc(fn)
	}
}
