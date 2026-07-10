package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var vendorCircuitBreaker sync.Map
var vendorHealthManager = NewHealthManager(context.Background())

func retryableStatus(s int) bool {
	return s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || (s >= 500 && s <= 599)
}

func transientErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

func IsConnReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func backoffDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > maxBackoff {
			return maxBackoff
		}
		return retryAfter
	}
	d := time.Duration(1<<(attempt-1)) * 500 * time.Millisecond
	if d > maxBackoff {
		d = maxBackoff
	}
	return d + time.Duration(rand.Intn(250))*time.Millisecond
}

func parseRetryAfter(resp *http.Response) time.Duration {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

func SendWithRetry(ctx context.Context, client *http.Client, vendor Vendor,
	newReq func(context.Context) (*http.Request, error)) (*http.Response, error) {

	var lastErr error
	var retryAfter time.Duration
	notify, _ := ctx.Value(CtxRetryNotify).(func(attempt, max int))

	for attempt := 0; attempt <= MaxRetries; attempt++ {
		if attempt > 0 {
			if notify != nil {
				notify(attempt, MaxRetries)
			}
			delay := backoffDelay(attempt, retryAfter)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		retryAfter = 0

		if hm := vendorHealthManager; hm != nil {
			if hm.GetStatus(vendor) == HealthCircuitOpen {
				return nil, fmt.Errorf("vendor %v unhealthy (circuit open)", vendor)
			}
		}

		if val, ok := vendorCircuitBreaker.Load(vendor); ok {
			if nextBreaker := val.(*CircuitBreaker); !nextBreaker.Check(nil) {
				return nil, fmt.Errorf("circuit breaker open for vendor %v", vendor)
			}
		}

		req, err := newReq(ctx)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			if !transientErr(err) {
				return nil, fmt.Errorf("request failed: %w", err)
			}
			if val, ok := vendorCircuitBreaker.Load(vendor); ok {
				val.(*CircuitBreaker).Check(err)
			}
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		retryAfter = parseRetryAfter(resp)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			if val, ok := vendorCircuitBreaker.Load(vendor); ok {
				val.(*CircuitBreaker).Success()
			}
			return nil, &AuthError{Vendor: vendor, Status: resp.StatusCode, HasKey: true}
		}
		apiErr := &APIError{Vendor: vendor, Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
		if !retryableStatus(resp.StatusCode) {
			return nil, apiErr
		}
		if val, ok := vendorCircuitBreaker.Load(vendor); ok {
			val.(*CircuitBreaker).Check(apiErr)
		}
		lastErr = apiErr
	}
	return nil, lastErr
}
