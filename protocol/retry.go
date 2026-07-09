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
	"syscall"
	"time"
)

const MaxRetries = 10
const maxBackoff = 15 * time.Second
const maxAuthRetries = 2

type AuthError struct {
	Vendor   Vendor
	Status   int
	HasKey   bool
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("auth error (vendor=%d, status=%d, has_key=%v)", e.Vendor, e.Status, e.HasKey)
}

type APIError struct {
	Vendor Vendor
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("api error (vendor=%d, status=%d)", e.Vendor, e.Status)
	}
	return fmt.Sprintf("api error (vendor=%d, status=%d): %s", e.Vendor, e.Status, e.Body)
}

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
	authRetries := 0

	for attempt := 0; attempt <= MaxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt, retryAfter)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		retryAfter = 0

		req, err := newReq(ctx)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			if !transientErr(err) {
				return nil, fmt.Errorf("request failed: %w", err)
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
			authErr := &AuthError{Vendor: vendor, Status: resp.StatusCode, HasKey: true}
			if authRetries < maxAuthRetries {
				authRetries++
				lastErr = authErr
				continue
			}
			return nil, authErr
		}
		apiErr := &APIError{Vendor: vendor, Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
		if !retryableStatus(resp.StatusCode) {
			return nil, apiErr
		}
		lastErr = apiErr
	}
	return nil, lastErr
}
