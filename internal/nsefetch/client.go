package nsefetch

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"
)

// NSEOptionChainResponse mirrors the NSE API JSON structure.
type NSEOptionChainResponse struct {
	Records struct {
		Timestamp       string            `json:"timestamp"`
		UnderlyingValue float64           `json:"underlyingValue"`
		Data            []NSEOptionRecord `json:"data"`
	} `json:"records"`
}

// NSEOptionRecord is one strike row from the API.
type NSEOptionRecord struct {
	StrikePrice float64        `json:"strikePrice"`
	ExpiryDates string         `json:"expiryDates"`
	CE          *NSEOptionData `json:"CE"`
	PE          *NSEOptionData `json:"PE"`
}

// NSEOptionData holds CE or PE data for a strike.
type NSEOptionData struct {
	StrikePrice     float64 `json:"strikePrice"`
	ExpiryDate      string  `json:"expiryDate"`
	Underlying      string  `json:"underlying"`
	OpenInterest    float64 `json:"openInterest"`
	TotalTradedVol  float64 `json:"totalTradedVolume"`
	ImpliedVol      float64 `json:"impliedVolatility"`
	LastPrice       float64 `json:"lastPrice"`
	BidPrice        float64 `json:"buyPrice1"`
	BidQty          float64 `json:"buyQuantity1"`
	AskPrice        float64 `json:"sellPrice1"`
	AskQty          float64 `json:"sellQuantity1"`
	UnderlyingValue float64 `json:"underlyingValue"`
	Change          float64 `json:"change"`
	PChange         float64 `json:"pchange"`
}

// Client fetches option chain data from NSE India.
type Client struct {
	http      *http.Client
	mu        sync.Mutex
	lastFetch time.Time
	minGap    time.Duration
	maxRetry  int
}

// NewClient creates a new NSE API client with cookie jar and rate limiting.
func NewClient() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		http: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		minGap:   2 * time.Second,
		maxRetry: 3,
	}
}

// warmSession hits the NSE homepage to get session cookies.
func (c *Client) warmSession() error {
	req, err := http.NewRequest("GET", "https://www.nseindia.com", nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("session warm-up failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body) // drain body

	if resp.StatusCode != 200 {
		return fmt.Errorf("session warm-up returned status %d", resp.StatusCode)
	}
	log.Println("[NSE] Session cookies acquired")
	return nil
}

// setHeaders adds realistic browser headers (NSE blocks bare requests).
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// Note: Do NOT set Accept-Encoding explicitly. Go's default transport
	// handles gzip transparently. Setting it manually (especially "br")
	// means we'd need to decompress ourselves.
	req.Header.Set("Referer", "https://www.nseindia.com/option-chain")
	req.Header.Set("Connection", "keep-alive")
}

// FetchOptionChain fetches data for a symbol+expiry from the NSE API.
// expiry format: "24-Feb-2026" (as NSE expects).
func (c *Client) FetchOptionChain(symbol, expiry string) (*NSEOptionChainResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Rate limit: wait for minimum gap
	elapsed := time.Since(c.lastFetch)
	if elapsed < c.minGap {
		time.Sleep(c.minGap - elapsed)
	}

	var lastErr error
	for attempt := 1; attempt <= c.maxRetry; attempt++ {
		// Warm session on first attempt or after failures
		if attempt == 1 || lastErr != nil {
			if err := c.warmSession(); err != nil {
				lastErr = err
				log.Printf("[NSE] Session warm-up failed (attempt %d/%d): %v", attempt, c.maxRetry, err)
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
				continue
			}
		}

		url := fmt.Sprintf("https://www.nseindia.com/api/option-chain-v3?type=Indices&symbol=%s&expiry=%s", symbol, expiry)
		log.Printf("[NSE] Fetching: %s (attempt %d/%d)", url, attempt, c.maxRetry)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		c.setHeaders(req)

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			log.Printf("[NSE] Request failed (attempt %d/%d): %v", attempt, c.maxRetry, err)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("API returned status %d", resp.StatusCode)
			log.Printf("[NSE] Non-200 status (attempt %d/%d): %d", attempt, c.maxRetry, resp.StatusCode)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}

		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		var result NSEOptionChainResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}

		c.lastFetch = time.Now()
		log.Printf("[NSE] Fetched %d records for %s expiry %s (spot=%.2f)",
			len(result.Records.Data), symbol, expiry, result.Records.UnderlyingValue)

		return &result, nil
	}

	return nil, fmt.Errorf("all %d attempts failed for %s/%s: %w", c.maxRetry, symbol, expiry, lastErr)
}
