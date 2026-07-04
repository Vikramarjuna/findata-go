// Package nse provides NSE India data provider
package nse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Vikramarjuna/findata-go/config"
	"github.com/Vikramarjuna/findata-go/internal/provider"
	"github.com/Vikramarjuna/findata-go/logger"
)

// nseBrowserUserAgent is what real browsers send. NSE's Akamai edge
// blocks non-browser User-Agents outright with a 403, so we override the
// findata-go default UA for NSE requests only.
const nseBrowserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// nseHomeURL is hit to seed cookies before an API request. NSE's API
// endpoints require the session cookies set by the main site.
const nseHomeURL = "https://www.nseindia.com/"

// nseWarmupTTL controls how often we re-hit the homepage. Session cookies
// expire; refreshing every few minutes keeps them alive.
const nseWarmupTTL = 5 * time.Minute

const (
	// BaseURL is the NSE India API base URL
	BaseURL = "https://www.nseindia.com/api"

	// QuoteEndpoint is the endpoint for equity quotes
	QuoteEndpoint = "/quote-equity"
)

// Provider implements the NSE data provider
type Provider struct {
	baseURL string // Allow overriding for testing

	// httpClient holds a cookie jar so session cookies obtained from the
	// homepage warmup are reused on subsequent API calls. NSE's edge
	// requires them.
	httpClient   *http.Client
	warmupMu     sync.Mutex
	lastWarmedAt time.Time
}

func newProvider(baseURL string) *Provider {
	jar, _ := cookiejar.New(nil)
	// Clone the configured HTTP client so we can attach a cookie jar without
	// mutating the shared global client used by other providers.
	base := config.GetHTTPClient()
	client := &http.Client{
		Transport:     base.Transport,
		CheckRedirect: base.CheckRedirect,
		Timeout:       base.Timeout,
		Jar:           jar,
	}
	return &Provider{
		baseURL:    baseURL,
		httpClient: client,
	}
}

// New creates a new NSE provider
func New() *Provider {
	return newProvider(BaseURL)
}

// NewWithBaseURL creates a new NSE provider with custom base URL (for testing)
func NewWithBaseURL(baseURL string) *Provider {
	return newProvider(baseURL)
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "NSE"
}

// SupportsSymbol checks if this provider can handle the given symbol
func (p *Provider) SupportsSymbol(symbol string) bool {
	// NSE symbols are typically all uppercase letters and numbers
	// No special characters like dots or dashes
	matched, _ := regexp.MatchString(`^[A-Z0-9&]+$`, symbol)
	return matched
}

// flexibleStringArray handles both string and []string from JSON
type flexibleStringArray []string

// UnmarshalJSON implements custom unmarshaling to handle both string and []string
func (f *flexibleStringArray) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as array first
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		// Filter out "NA" values
		filtered := make([]string, 0, len(arr))
		for _, val := range arr {
			if val != "NA" && val != "" {
				filtered = append(filtered, val)
			}
		}
		*f = filtered
		return nil
	}

	// If that fails, try as a single string
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		// Ignore "NA" and empty strings
		if str != "" && str != "NA" {
			*f = []string{str}
		} else {
			*f = []string{}
		}
		return nil
	}

	// If both fail, return empty array
	*f = []string{}
	return nil
}

// nseQuoteResponse represents the raw NSE API response
type nseQuoteResponse struct {
	Info struct {
		Symbol      string `json:"symbol"`
		CompanyName string `json:"companyName"`
		Industry    string `json:"industry"`
	} `json:"info"`
	Metadata struct {
		PdSectorIndAll flexibleStringArray `json:"pdSectorIndAll"`
	} `json:"metadata"`
	PriceInfo struct {
		LastPrice       float64 `json:"lastPrice"`
		Change          float64 `json:"change"`
		PChange         float64 `json:"pChange"`
		PreviousClose   float64 `json:"previousClose"`
		Open            float64 `json:"open"`
		Close           any     `json:"close"`
		IntraDayHighLow struct {
			Max float64 `json:"max"`
			Min float64 `json:"min"`
		} `json:"intraDayHighLow"`
		WeekHighLow struct {
			Max float64 `json:"max"`
			Min float64 `json:"min"`
		} `json:"weekHighLow"`
	} `json:"priceInfo"`
	PreOpenMarket struct {
		TotalTradedVolume float64 `json:"totalTradedVolume"`
		TotalTradedValue  float64 `json:"totalTradedValue"`
	} `json:"preOpenMarket"`
	IndustryInfo struct {
		Macro    string `json:"macro"`
		Sector   string `json:"sector"`
		Industry string `json:"industry"`
	} `json:"industryInfo"`
}

// Get fetches a quote for the given symbol from NSE India
func (p *Provider) Get(symbol string) (*provider.Quote, error) {
	// Clean up symbol (remove any whitespace, convert to uppercase)
	symbol = strings.TrimSpace(strings.ToUpper(symbol))
	logger.Debug("fetching NSE quote", "symbol", symbol, "provider", p.Name())

	// Fetch data from NSE API
	result, err := p.fetchNSEData(symbol)
	if err != nil {
		return nil, err
	}

	// Convert to provider.Quote
	quote := p.mapToQuote(result)
	logger.Info("successfully fetched NSE quote", "symbol", symbol, "price", quote.LastPrice, "currency", quote.Currency)
	return quote, nil
}

// warmUp fetches the NSE homepage so the cookie jar picks up the session
// cookies the API endpoints require. Safe to call repeatedly; hits the
// network at most once per nseWarmupTTL unless force is set.
func (p *Provider) warmUp(force bool) error {
	p.warmupMu.Lock()
	defer p.warmupMu.Unlock()

	if !force && !p.lastWarmedAt.IsZero() && time.Since(p.lastWarmedAt) < nseWarmupTTL {
		return nil
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", nseHomeURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("build warmup request: %w", err)
	}
	req.Header.Set("User-Agent", nseBrowserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("warmup request failed: %w", err)
	}
	// We only care about the Set-Cookie side effect on the jar; drain and
	// close.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("warmup returned status %d", resp.StatusCode)
	}
	p.lastWarmedAt = time.Now()
	return nil
}

func (p *Provider) fetchNSEData(symbol string) (*nseQuoteResponse, error) {
	apiURL, err := url.Parse(p.baseURL + QuoteEndpoint)
	if err != nil {
		return nil, &provider.Error{
			Message:  "failed to parse base URL: " + err.Error(),
			Provider: p.Name(),
		}
	}

	query := apiURL.Query()
	query.Set("symbol", symbol)
	apiURL.RawQuery = query.Encode()

	logger.Debug("creating NSE API request", "url", apiURL.String(), "symbol", symbol)

	// One retry: if the first call gets blocked, re-warm cookies and try
	// again. Anything else is a real failure.
	var lastBody []byte
	var lastStatus int
	for attempt := 0; attempt < 2; attempt++ {
		if err := p.warmUp(attempt > 0); err != nil {
			logger.Warn("NSE warmup failed", "error", err, "symbol", symbol, "attempt", attempt)
			// A warmup failure isn't fatal on its own — try the API and let
			// the status-code branch decide.
		}

		req, err := http.NewRequestWithContext(context.Background(), "GET", apiURL.String(), http.NoBody)
		if err != nil {
			logger.Error("failed to create NSE request", "error", err, "symbol", symbol)
			return nil, &provider.Error{
				Message:  "failed to create request: " + err.Error(),
				Provider: p.Name(),
			}
		}

		// Browser-like headers. NSE's edge (Akamai) rejects the default
		// findata-go User-Agent outright, so override it here.
		req.Header.Set("User-Agent", nseBrowserUserAgent)
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Referer", "https://www.nseindia.com/get-quotes/equity?symbol="+url.QueryEscape(symbol))
		req.Header.Set("X-Requested-With", "XMLHttpRequest")

		logger.Debug("sending HTTP request to NSE", "symbol", symbol, "attempt", attempt)
		resp, err := p.httpClient.Do(req)
		if err != nil {
			logger.Error("NSE HTTP request failed", "error", err, "symbol", symbol, "url", apiURL.String())
			return nil, &provider.Error{
				Message:  "HTTP request failed: " + err.Error(),
				Provider: p.Name(),
			}
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			logger.Error("failed to read NSE response body", "error", readErr, "symbol", symbol)
			return nil, &provider.Error{
				Message:  "failed to read response: " + readErr.Error(),
				Provider: p.Name(),
			}
		}

		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			// Cookies likely stale/missing — force a re-warm and retry once.
			lastBody, lastStatus = body, resp.StatusCode
			logger.Warn("NSE returned access denied, will retry with fresh cookies",
				"status_code", resp.StatusCode, "symbol", symbol, "attempt", attempt)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			logger.Warn("NSE API returned non-OK status", "status_code", resp.StatusCode, "symbol", symbol, "response", string(body))
			return nil, &provider.Error{
				Message:  "NSE API returned error: " + string(body),
				Code:     resp.StatusCode,
				Provider: p.Name(),
			}
		}

		logger.Debug("parsing NSE JSON response", "symbol", symbol, "body_size", len(body))
		var result nseQuoteResponse
		if err := json.Unmarshal(body, &result); err != nil {
			logger.Error("failed to parse NSE JSON", "error", err, "symbol", symbol)
			return nil, &provider.Error{
				Message:  "failed to parse JSON: " + err.Error(),
				Provider: p.Name(),
			}
		}
		return &result, nil
	}

	return nil, &provider.Error{
		Message:  "NSE API returned error: " + string(lastBody),
		Code:     lastStatus,
		Provider: p.Name(),
	}
}

func (p *Provider) mapToQuote(result *nseQuoteResponse) *provider.Quote {
	quote := &provider.Quote{
		Symbol:        result.Info.Symbol,
		Exchange:      config.ExchangeNSE,
		CompanyName:   result.Info.CompanyName,
		Industry:      result.IndustryInfo.Industry,
		Sector:        result.IndustryInfo.Sector,
		LastPrice:     result.PriceInfo.LastPrice,
		Currency:      "INR",
		Change:        result.PriceInfo.Change,
		PChange:       result.PriceInfo.PChange,
		PreviousClose: result.PriceInfo.PreviousClose,
		Open:          result.PriceInfo.Open,
		DayHigh:       result.PriceInfo.IntraDayHighLow.Max,
		DayLow:        result.PriceInfo.IntraDayHighLow.Min,
		YearHigh:      result.PriceInfo.WeekHighLow.Max,
		YearLow:       result.PriceInfo.WeekHighLow.Min,
		Volume:        result.PreOpenMarket.TotalTradedVolume,
		Value:         result.PreOpenMarket.TotalTradedValue,
		Indices:       []string(result.Metadata.PdSectorIndAll),
	}

	// Populate metadata with market cap classification
	if quote.Metadata == nil {
		quote.Metadata = make(map[string]string)
	}
	quote.Metadata["market_cap"] = determineMarketCap(quote.Indices)

	return quote
}

// determineMarketCap determines the market cap category based on NSE indices membership
func determineMarketCap(indices []string) string {
	hasLargeCap := false
	hasMidCap := false
	hasSmallCap := false

	for _, idx := range indices {
		switch idx {
		case "NIFTY 50", "NIFTY 100", "NIFTY100 EQUAL WEIGHT", "NIFTY100 ESG",
			"NIFTY100 ENHANCED ESG", "NIFTY100 LIQUID 15", "NIFTY100 LOW VOLATILITY 30",
			"NIFTY50 EQUAL WEIGHT", "NIFTY TOP 10 EQUAL WEIGHT", "NIFTY TOP 15 EQUAL WEIGHT",
			"NIFTY TOP 20 EQUAL WEIGHT":
			hasLargeCap = true
		case "NIFTY MIDCAP 50", "NIFTY MIDCAP 100", "NIFTY MIDCAP 150",
			"NIFTY MIDCAP SELECT", "NIFTY MIDCAP150 QUALITY 50":
			hasMidCap = true
		case "NIFTY SMALLCAP 50", "NIFTY SMALLCAP 100", "NIFTY SMALLCAP 250",
			"NIFTY SMLCAP 50", "NIFTY SMLCAP 100", "NIFTY SMLCAP 250":
			hasSmallCap = true
		}
	}

	// Priority: Large Cap > Mid Cap > Small Cap
	if hasLargeCap {
		return "Large Cap"
	}
	if hasMidCap {
		return "Mid Cap"
	}
	if hasSmallCap {
		return "Small Cap"
	}
	return "Other"
}

// GetMultiple fetches quotes for multiple symbols
func (p *Provider) GetMultiple(symbols []string) (map[string]*provider.Quote, []error) {
	quotes := make(map[string]*provider.Quote)
	var errors []error

	for _, symbol := range symbols {
		quote, err := p.Get(symbol)
		if err != nil {
			errors = append(errors, fmt.Errorf("%s: %w", symbol, err))
			continue
		}
		quotes[symbol] = quote
	}

	return quotes, errors
}
