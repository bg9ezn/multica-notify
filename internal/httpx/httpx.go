// Package httpx holds the one piece of HTTP plumbing every channel shares:
// a JSON POST with bounded retries. Transient failures (network errors and
// 5xx) retry; 4xx does not — a wrong request stays wrong, and repeating it
// only burns the receiver's rate limit.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// StatusError is a definitive non-2xx answer.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %d: %s", e.Code, truncate(e.Body))
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// PostJSON posts payload as application/json. retries is the number of EXTRA
// attempts after the first; backoff is attempt × 500ms. The response body is
// read (bounded) and closed; only the error is reported.
func PostJSON(ctx context.Context, client *http.Client, url string, payload any, headers map[string]string, retries int) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("POST %s: %w", url, err)
			continue
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return nil
		case resp.StatusCode >= 500:
			lastErr = &StatusError{Code: resp.StatusCode, Body: string(respBody)}
		default:
			return &StatusError{Code: resp.StatusCode, Body: string(respBody)}
		}
	}
	return fmt.Errorf("POST %s failed after %d attempts: %w", url, retries+1, lastErr)
}
