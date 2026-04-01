package espn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Client wraps HTTP with rate limiting and retries for ESPN APIs.
type Client struct {
	http    *http.Client
	mu      sync.Mutex
	lastReq time.Time
	minGap  time.Duration
}

// NewClient creates an ESPN API client with the given minimum gap between requests.
func NewClient(minGap time.Duration) *Client {
	return &Client{
		http: &http.Client{Timeout: 15 * time.Second},
		minGap: minGap,
	}
}

// FetchJSON fetches a URL and decodes the JSON response into T.
func FetchJSON[T any](ctx context.Context, c *Client, url string) (T, error) {
	var zero T
	c.rateLimit()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, err
	}

	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = c.http.Do(req)
		if err != nil {
			if attempt < 2 {
				select {
				case <-ctx.Done():
					return zero, ctx.Err()
				case <-time.After(time.Duration(attempt+1) * time.Second):
				}
				continue
			}
			return zero, err
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			if attempt < 2 {
				select {
				case <-ctx.Done():
					return zero, ctx.Err()
				case <-time.After(time.Duration(attempt+1) * time.Second):
				}
				continue
			}
			return zero, fmt.Errorf("http %d from %s after retries", resp.StatusCode, url)
		}
		break
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return zero, fmt.Errorf("http %d from %s: %s", resp.StatusCode, url, string(body))
	}

	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return zero, fmt.Errorf("decode %s: %w", url, err)
	}
	return out, nil
}

func (c *Client) rateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := time.Since(c.lastReq)
	if elapsed < c.minGap {
		time.Sleep(c.minGap - elapsed)
	}
	c.lastReq = time.Now()
}
