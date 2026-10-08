package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	DefaultBaseURL = "https://gamma-api.polymarket.com"
	pageLimit      = 100 // Gamma API maximum
	maxAttempts    = 5
)

// Client reads markets from the Polymarket Gamma API.
type Client struct {
	baseURL    string
	http       *http.Client
	retryDelay time.Duration
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		http:       &http.Client{Timeout: 15 * time.Second},
		retryDelay: 2 * time.Second,
	}
}

type keysetPage struct {
	Markets    []apiMarket `json:"markets"`
	NextCursor string      `json:"next_cursor"`
}

// MarketsPage returns one page of markets from /markets/keyset and the cursor
// for the next page ("" when this was the last page). The API never mixes
// statuses: closed=false returns only open markets, closed=true only closed.
func (c *Client) MarketsPage(ctx context.Context, closed bool, cursor string) ([]Market, string, error) {
	q := url.Values{}
	q.Set("limit", fmt.Sprint(pageLimit))
	q.Set("include_tag", "true")
	q.Set("closed", fmt.Sprint(closed))
	if cursor != "" {
		q.Set("after_cursor", cursor)
	}

	var page keysetPage
	if err := c.getJSON(ctx, "/markets/keyset?"+q.Encode(), &page); err != nil {
		return nil, "", err
	}

	markets := make([]Market, 0, len(page.Markets))
	for _, a := range page.Markets {
		m, err := a.toMarket()
		if err != nil {
			return nil, "", err
		}
		markets = append(markets, m)
	}

	next := page.NextCursor
	if len(page.Markets) == 0 {
		next = ""
	}
	return markets, next, nil
}

// AllMarkets walks every page of open (closed=false) or closed (closed=true)
// markets and passes each page to fn. It stops on the first error from the
// API or from fn.
func (c *Client) AllMarkets(ctx context.Context, closed bool, fn func(page []Market) error) error {
	cursor := ""
	for {
		markets, next, err := c.MarketsPage(ctx, closed, cursor)
		if err != nil {
			return err
		}
		if len(markets) > 0 {
			if err := fn(markets); err != nil {
				return err
			}
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

// errRetryable marks failures worth another attempt (network, 429, 5xx).
var errRetryable = errors.New("retryable")

func (c *Client) getJSON(ctx context.Context, path string, dst any) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// Exponential backoff: retryDelay, 2x, 4x, 8x.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.retryDelay << (attempt - 2)):
			}
		}

		lastErr = c.getOnce(ctx, path, dst)
		if lastErr == nil || !errors.Is(lastErr, errRetryable) {
			return lastErr
		}
	}
	return fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

func (c *Client) getOnce(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("GET %s: %w: %w", path, errRetryable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("GET %s: status %d: %w", path, resp.StatusCode, errRetryable)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, body)
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("GET %s: decode: %w", path, err)
	}
	return nil
}
