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

	"github.com/rs/zerolog"
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
	log        zerolog.Logger
}

func NewClient(baseURL string, log zerolog.Logger) *Client {
	return &Client{
		baseURL:    baseURL,
		http:       &http.Client{Timeout: 15 * time.Second},
		retryDelay: 2 * time.Second,
		log:        log.With().Str("component", "polymarket").Logger(),
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
	return c.marketsPage(ctx, closed, cursor, false)
}

// marketsPage fetches one keyset page; newestFirst sorts by updatedAt, newest first.
func (c *Client) marketsPage(ctx context.Context, closed bool, cursor string, newestFirst bool) ([]Market, string, error) {
	q := url.Values{}
	q.Set("limit", fmt.Sprint(pageLimit))
	q.Set("include_tag", "true")
	q.Set("closed", fmt.Sprint(closed))
	if newestFirst {
		q.Set("order", "updatedAt")
		q.Set("ascending", "false")
	}
	if cursor != "" {
		q.Set("after_cursor", cursor)
	}

	var page keysetPage
	if err := c.getJSON(ctx, "/markets/keyset", q, &page); err != nil {
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
	return c.walk(ctx, closed, false, func(page []Market) (bool, error) {
		return true, fn(page)
	})
}

// RecentlyUpdated walks open or closed markets from the most recently updated
// backwards and passes each page to fn until fn returns more=false, the pages
// run out, or an error occurs.
func (c *Client) RecentlyUpdated(ctx context.Context, closed bool, fn func(page []Market) (more bool, err error)) error {
	return c.walk(ctx, closed, true, fn)
}

func (c *Client) walk(ctx context.Context, closed, newestFirst bool, fn func(page []Market) (bool, error)) error {
	cursor := ""
	for pageNum := 1; ; pageNum++ {
		started := time.Now()
		markets, next, err := c.marketsPage(ctx, closed, cursor, newestFirst)
		if err != nil {
			return fmt.Errorf("page %d: %w", pageNum, err)
		}
		c.log.Debug().
			Bool("closed", closed).
			Int("page", pageNum).
			Int("markets", len(markets)).
			Dur("took", time.Since(started)).
			Msg("Markets page fetched")
		if len(markets) > 0 {
			more, err := fn(markets)
			if err != nil {
				return err
			}
			if !more {
				return nil
			}
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

// retryableError is a failure worth another attempt (network error, 429, 5xx).
type retryableError struct {
	status int   // HTTP status, 0 for network errors
	err    error // network error, nil for bad statuses
}

func (e *retryableError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("status %d", e.status)
}

func (e *retryableError) Unwrap() error { return e.err }

// getJSON requests path with query and decodes the JSON response into dst,
// retrying retryable failures with exponential backoff. Errors mention only
// the path: the query holds a long keyset cursor that is useless in logs.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, dst any) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = c.getOnce(ctx, path, query, dst)

		var retryable *retryableError
		if lastErr == nil || !errors.As(lastErr, &retryable) {
			return lastErr
		}
		if attempt == maxAttempts {
			break
		}

		// Exponential backoff: retryDelay, 2x, 4x, 8x.
		delay := c.retryDelay << (attempt - 1)
		ev := c.log.Warn().
			Str("path", path).
			Int("attempt", attempt).
			Int("max_attempts", maxAttempts).
			Dur("retry_in", delay)
		if retryable.status != 0 {
			ev = ev.Int("status", retryable.status)
		} else {
			ev = ev.Err(retryable.err)
		}
		ev.Msg("Polymarket request failed, retrying")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("GET %s: after %d attempts: %w", path, maxAttempts, lastErr)
}

func (c *Client) getOnce(ctx context.Context, path string, query url.Values, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// *url.Error repeats the full URL (with the cursor); keep only the cause.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return &retryableError{err: err}
	}
	// Close errors after reading carry no information about the data.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return &retryableError{status: resp.StatusCode}
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
