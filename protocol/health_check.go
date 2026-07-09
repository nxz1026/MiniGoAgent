package protocol

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type HealthStatus int

const (
	HealthUnknown HealthStatus = iota
	HealthHealthy
	HealthUnhealthy
	HealthCircuitOpen
)

type HealthChecker struct {
	vendor             Vendor
	client             *http.Client
	endpoint           string
	interval           time.Duration
	timeout            time.Duration
	unhealthyThreshold int
	mu                 sync.RWMutex
	status             HealthStatus
	lastCheck          time.Time
	breaker            *CircuitBreaker
	stopCh             chan struct{}
}

func NewHealthChecker(vendor Vendor, endpoint string, interval time.Duration, breaker *CircuitBreaker) *HealthChecker {
	return &HealthChecker{
		vendor:             vendor,
		client:             NewHTTPClient(5 * time.Second),
		endpoint:           endpoint,
		interval:           interval,
		timeout:            5 * time.Second,
		unhealthyThreshold: 3,
		breaker:            breaker,
		status:             HealthUnknown,
		stopCh:             make(chan struct{}),
	}
}

func (h *HealthChecker) Start(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.check(ctx)
		}
	}
}

func (h *HealthChecker) Stop() {
	close(h.stopCh)
}

func (h *HealthChecker) Status() HealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.status
}

func (h *HealthChecker) check(ctx context.Context) {
	reqCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, h.endpoint, nil)
	if err != nil {
		h.recordFailure(err)
		return
	}

	resp, err := h.client.Do(req)
	if err != nil {
		h.recordFailure(err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
		h.recordSuccess()
	} else {
		h.recordFailure(fmt.Errorf("unexpected status: %d", resp.StatusCode))
	}
}

func (h *HealthChecker) recordSuccess() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = HealthHealthy
	h.lastCheck = time.Now()
	if h.breaker != nil {
		h.breaker.Success()
	}
}

func (h *HealthChecker) recordFailure(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = HealthUnhealthy
	h.lastCheck = time.Now()
	if h.breaker != nil {
		h.breaker.Check(err)
	}
}
