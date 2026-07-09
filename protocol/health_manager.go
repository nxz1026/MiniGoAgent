package protocol

import (
	"context"
	"sync"
	"time"
)

type HealthManager struct {
	checkers map[Vendor]*HealthChecker
	breakers map[Vendor]*CircuitBreaker
	mu       sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewHealthManager(ctx context.Context) *HealthManager {
	ctx, cancel := context.WithCancel(ctx)
	return &HealthManager{
		checkers: make(map[Vendor]*HealthChecker),
		breakers: make(map[Vendor]*CircuitBreaker),
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (m *HealthManager) Register(vendor Vendor, endpoint string, interval time.Duration, breaker *CircuitBreaker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.checkers[vendor]; ok {
		return
	}
	checker := NewHealthChecker(vendor, endpoint, interval, breaker)
	m.checkers[vendor] = checker
	m.breakers[vendor] = breaker
	go checker.Start(m.ctx)
}

func (m *HealthManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, checker := range m.checkers {
		checker.Stop()
	}
}

func (m *HealthManager) GetStatus(vendor Vendor) HealthStatus {
	m.mu.RLock()
	checker, ok := m.checkers[vendor]
	m.mu.RUnlock()
	if !ok {
		return HealthUnknown
	}
	status := checker.Status()
	if status == HealthUnhealthy {
		if cb, ok := m.breakers[vendor]; ok && cb != nil {
			if !cb.Check(nil) {
				return HealthCircuitOpen
			}
		}
	}
	return status
}

func defaultHealthEndpoint(v Vendor, baseURL string) string {
	switch v {
	case VendorDeepSeek, VendorZhipu, VendorMiniMax, VendorLongCat, VendorOllamaCloud, VendorMiMo:
		return baseURL + "/models"
	case VendorUnspecified:
		return baseURL + "/models"
	}
	return ""
}
