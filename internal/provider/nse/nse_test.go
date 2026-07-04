package nse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vikramarjuna/findata-go/config"
	"github.com/Vikramarjuna/findata-go/internal/provider"
)

const (
	testProviderName = "NSE"
	testSymbol       = "RELIANCE"
	testCurrency     = "INR"
)

func TestProviderName(t *testing.T) {
	p := New()
	if p.Name() != testProviderName {
		t.Errorf("Name() = %v, want %s", p.Name(), testProviderName)
	}
}

func TestSupportsSymbol(t *testing.T) {
	p := New()

	tests := []struct {
		name   string
		symbol string
		want   bool
	}{
		{"Valid uppercase", testSymbol, true},
		{"Valid with numbers", "NIFTY50", true},
		{"Valid with ampersand", "M&M", true},
		{"Invalid with dot", "BRK.A", false},
		{"Invalid with dash", "BRK-A", false},
		{"Invalid lowercase", "reliance", false},
		{"Invalid with space", "REL IANCE", false},
		{"Empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.SupportsSymbol(tt.symbol)
			if got != tt.want {
				t.Errorf("SupportsSymbol(%v) = %v, want %v", tt.symbol, got, tt.want)
			}
		})
	}
}

func TestDetermineMarketCap(t *testing.T) {
	tests := []struct {
		name    string
		indices []string
		want    string
	}{
		{
			name:    "Large Cap - NIFTY 50",
			indices: []string{"NIFTY 50"},
			want:    "Large Cap",
		},
		{
			name:    "Large Cap - NIFTY 100",
			indices: []string{"NIFTY 100", "NIFTY MIDCAP 100"},
			want:    "Large Cap", // Large cap takes priority
		},
		{
			name:    "Mid Cap",
			indices: []string{"NIFTY MIDCAP 50", "NIFTY MIDCAP 100"},
			want:    "Mid Cap",
		},
		{
			name:    "Small Cap",
			indices: []string{"NIFTY SMALLCAP 100"},
			want:    "Small Cap",
		},
		{
			name:    "Other - No matching indices",
			indices: []string{"NIFTY BANK", "NIFTY IT"},
			want:    "Other",
		},
		{
			name:    "Empty indices",
			indices: []string{},
			want:    "Other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineMarketCap(tt.indices)
			if got != tt.want {
				t.Errorf("determineMarketCap(%v) = %v, want %v", tt.indices, got, tt.want)
			}
		})
	}
}

func TestGet_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()
	quote, err := p.Get(testSymbol)
	if err != nil {
		t.Fatalf("Get(RELIANCE) error = %v", err)
	}

	// Validate quote structure
	if quote.Symbol != testSymbol {
		t.Errorf("Symbol = %v, want RELIANCE", quote.Symbol)
	}
	if quote.Exchange != config.ExchangeNSE {
		t.Errorf("Exchange = %v, want NSE", quote.Exchange)
	}
	if quote.Currency != testCurrency {
		t.Errorf("Currency = %v, want INR", quote.Currency)
	}
	if quote.LastPrice <= 0 {
		t.Errorf("LastPrice = %v, want > 0", quote.LastPrice)
	}
	if quote.CompanyName == "" {
		t.Error("CompanyName should not be empty")
	}
	if quote.Sector == "" {
		t.Error("Sector should not be empty")
	}
}

func TestGet_InvalidSymbol(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()
	quote, err := p.Get("INVALIDSYMBOL123456")
	// NSE may return partial data or error for invalid symbols
	// We just verify it doesn't crash
	if err != nil {
		t.Logf("Got expected error for invalid symbol: %v", err)
	} else if quote != nil {
		t.Logf("NSE returned partial data for invalid symbol (this is OK)")
	}
}

func TestGetMultiple_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()
	symbols := []string{"TCS", "INFY"}
	quotes, errors := p.GetMultiple(symbols)

	if len(errors) > 0 {
		t.Logf("Encountered %d errors: %v", len(errors), errors)
	}

	if len(quotes) == 0 {
		t.Fatal("GetMultiple should return at least one quote")
	}

	for symbol, quote := range quotes {
		if quote.Symbol != symbol {
			t.Errorf("Quote symbol = %v, want %v", quote.Symbol, symbol)
		}
		if quote.Exchange != config.ExchangeNSE {
			t.Errorf("Exchange = %v, want NSE", quote.Exchange)
		}
		if quote.Currency != testCurrency {
			t.Errorf("Currency = %v, want INR", quote.Currency)
		}
		if quote.LastPrice <= 0 {
			t.Errorf("LastPrice for %s = %v, want > 0", symbol, quote.LastPrice)
		}
	}
}

func TestGetMultiple_WithErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()
	symbols := []string{"TCS", "INVALIDSYMBOL123", "INFY"}
	quotes, errors := p.GetMultiple(symbols)

	// NSE may or may not return errors for invalid symbols
	// Just verify we get some valid quotes
	if len(quotes) < 2 {
		t.Errorf("GetMultiple should return at least 2 valid quotes, got %d", len(quotes))
	}

	if len(errors) > 0 {
		t.Logf("Got %d errors (this is OK): %v", len(errors), errors)
	}
}

func TestSymbolCleaning(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()

	// Test that symbols are cleaned (trimmed, uppercased)
	tests := []struct {
		name   string
		symbol string
	}{
		{"With spaces", " RELIANCE "},
		{"Lowercase", "reliance"},
		{"Mixed case", "Reliance"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quote, err := p.Get(tt.symbol)
			if err != nil {
				t.Skipf("Skipping due to error: %v", err)
				return
			}
			if quote.Symbol != testSymbol {
				t.Errorf("Symbol = %v, want RELIANCE", quote.Symbol)
			}
		})
	}
}

func TestQuoteFields(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()
	quote, err := p.Get(testSymbol)
	if err != nil {
		t.Fatalf("Get(RELIANCE) error = %v", err)
	}

	// Test all fields are populated correctly
	tests := []struct {
		name  string
		value any
		check func(any) bool
	}{
		{"Symbol", quote.Symbol, func(v any) bool { return v.(string) == testSymbol }},
		{"Exchange", quote.Exchange, func(v any) bool { return v.(config.Exchange) == config.ExchangeNSE }},
		{"Currency", quote.Currency, func(v any) bool { return v.(string) == testCurrency }},
		{"CompanyName", quote.CompanyName, func(v any) bool { return v.(string) != "" }},
		{"Industry", quote.Industry, func(v any) bool { return v.(string) != "" }},
		{"Sector", quote.Sector, func(v any) bool { return v.(string) != "" }},
		{"LastPrice", quote.LastPrice, func(v any) bool { return v.(float64) > 0 }},
		{"PreviousClose", quote.PreviousClose, func(v any) bool { return v.(float64) > 0 }},
		{"Open", quote.Open, func(v any) bool { return v.(float64) >= 0 }},
		{"DayHigh", quote.DayHigh, func(v any) bool { return v.(float64) >= 0 }},
		{"DayLow", quote.DayLow, func(v any) bool { return v.(float64) >= 0 }},
		{"YearHigh", quote.YearHigh, func(v any) bool { return v.(float64) >= 0 }},
		{"YearLow", quote.YearLow, func(v any) bool { return v.(float64) >= 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.check(tt.value) {
				t.Errorf("%s validation failed: %v", tt.name, tt.value)
			}
		})
	}
}

func TestGetMultiple_EmptyList(t *testing.T) {
	p := New()
	quotes, errors := p.GetMultiple([]string{})

	if len(quotes) != 0 {
		t.Errorf("GetMultiple([]) should return empty map, got %d quotes", len(quotes))
	}
	if len(errors) != 0 {
		t.Errorf("GetMultiple([]) should return no errors, got %d errors", len(errors))
	}
}

func TestGetMultiple_SingleSymbol(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()
	quotes, errors := p.GetMultiple([]string{"TCS"})

	if len(errors) > 0 {
		t.Logf("Got errors: %v", errors)
	}

	if len(quotes) != 1 {
		t.Errorf("GetMultiple([TCS]) should return 1 quote, got %d", len(quotes))
	}

	if quote, ok := quotes["TCS"]; ok {
		if quote.Symbol != "TCS" {
			t.Errorf("Symbol = %v, want TCS", quote.Symbol)
		}
	}
}

func TestProviderInterface(_ *testing.T) {
	// Verify Provider implements provider.Provider interface
	var _ provider.Provider = (*Provider)(nil)
}

func TestConstants(t *testing.T) {
	if BaseURL == "" {
		t.Error("BaseURL should not be empty")
	}
	if QuoteEndpoint == "" {
		t.Error("QuoteEndpoint should not be empty")
	}

	expectedURL := "https://www.nseindia.com/api"
	if BaseURL != expectedURL {
		t.Errorf("BaseURL = %v, want %v", BaseURL, expectedURL)
	}

	expectedEndpoint := "/quote-equity"
	if QuoteEndpoint != expectedEndpoint {
		t.Errorf("QuoteEndpoint = %v, want %v", QuoteEndpoint, expectedEndpoint)
	}
}

func TestSupportsSymbol_EdgeCases(t *testing.T) {
	p := New()

	tests := []struct {
		name   string
		symbol string
		want   bool
	}{
		{"Single letter", "A", true},
		{"Two letters", "AB", true},
		{"With ampersand", "M&M", true},
		{"With ampersand middle", "M&MFIN", true},
		{"All numbers", "123", true},
		{"Mixed alphanumeric", "NIFTY50", true},
		{"Underscore", "TEST_SYMBOL", false},
		{"Hyphen", "TEST-SYMBOL", false},
		{"Dot", "TEST.SYMBOL", false},
		{"Lowercase single", "a", false},
		{"Special chars", "TEST@SYMBOL", false},
		{"Unicode", "TEST™", false},
		{"Whitespace only", "   ", false},
		{"Tab", "\t", false},
		{"Newline", "\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.SupportsSymbol(tt.symbol)
			if got != tt.want {
				t.Errorf("SupportsSymbol(%q) = %v, want %v", tt.symbol, got, tt.want)
			}
		})
	}
}

func TestGet_SymbolNormalization(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()

	tests := []struct {
		name       string
		input      string
		wantSymbol string
	}{
		{"Leading space", " TCS", "TCS"},
		{"Trailing space", "TCS ", "TCS"},
		{"Both spaces", " TCS ", "TCS"},
		{"Multiple spaces", "  TCS  ", "TCS"},
		{"Lowercase", "tcs", "TCS"},
		{"Mixed case", "TcS", "TCS"},
		{"Tab prefix", "\tTCS", "TCS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quote, err := p.Get(tt.input)
			if err != nil {
				t.Skipf("Skipping due to error: %v", err)
				return
			}
			if quote.Symbol != tt.wantSymbol {
				t.Errorf("Symbol = %v, want %v", quote.Symbol, tt.wantSymbol)
			}
		})
	}
}

func TestGet_HTTPError(t *testing.T) {
	// Create a test server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Symbol not found"))
	}))
	defer server.Close()

	// Create provider with test server URL
	p := NewWithBaseURL(server.URL)
	_, err := p.Get("TEST")

	if err == nil {
		t.Error("Expected error for 404 response")
	}

	// Verify error contains status code
	provErr, ok := err.(*provider.Error)
	if !ok {
		t.Fatalf("Expected *provider.Error, got %T", err)
	}

	if provErr.Code != http.StatusNotFound {
		t.Errorf("Error code = %d, want %d", provErr.Code, http.StatusNotFound)
	}

	if provErr.Provider != testProviderName {
		t.Errorf("Provider = %s, want %s", provErr.Provider, testProviderName)
	}
}

func TestGet_InvalidJSON(t *testing.T) {
	// Create a test server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	// Create provider with test server URL
	p := NewWithBaseURL(server.URL)
	_, err := p.Get("TEST")

	if err == nil {
		t.Error("Expected error for invalid JSON")
	}

	// Verify error message mentions JSON parsing
	if err != nil && !contains(err.Error(), "parse") && !contains(err.Error(), "JSON") {
		t.Errorf("Error should mention JSON parsing: %v", err)
	}
}

func TestGet_HTTP500Error(t *testing.T) {
	// Create a test server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	p := NewWithBaseURL(server.URL)
	_, err := p.Get("TEST")

	if err == nil {
		t.Error("Expected error for 500 response")
	}

	provErr, ok := err.(*provider.Error)
	if !ok {
		t.Fatalf("Expected *provider.Error, got %T", err)
	}

	if provErr.Code != http.StatusInternalServerError {
		t.Errorf("Error code = %d, want %d", provErr.Code, http.StatusInternalServerError)
	}
}

func TestGet_EmptyResponse(t *testing.T) {
	// Create a test server that returns empty JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	p := NewWithBaseURL(server.URL)
	quote, err := p.Get("TEST")

	// Should not error on empty but valid JSON
	if err != nil {
		t.Errorf("Unexpected error for empty JSON: %v", err)
	}

	if quote == nil {
		t.Error("Quote should not be nil")
	}
}

func mockValidNSEResponse() string {
	return `{
		"info": {
			"symbol": "TEST",
			"companyName": "Test Company",
			"industry": "Technology"
		},
		"metadata": {
			"pdSectorIndAll": ["NIFTY 50", "NIFTY IT"]
		},
		"priceInfo": {
			"lastPrice": 100.50,
			"change": 2.50,
			"pChange": 2.55,
			"previousClose": 98.00,
			"open": 99.00,
			"intraDayHighLow": {
				"max": 101.00,
				"min": 98.50
			},
			"weekHighLow": {
				"max": 120.00,
				"min": 80.00
			}
		},
		"preOpenMarket": {
			"totalTradedVolume": 1000000,
			"totalTradedValue": 100500000
		},
		"industryInfo": {
			"macro": "Technology",
			"sector": "IT Services",
			"industry": "Software"
		}
	}`
}

func TestGet_ValidMockResponse(t *testing.T) {
	// Create a test server that returns valid NSE-like JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers — Accept negotiates JSON but includes
		// browser-style fallbacks to avoid Akamai edge blocking.
		if !strings.Contains(r.Header.Get("Accept"), "application/json") {
			t.Errorf("Accept header = %s, want to contain application/json", r.Header.Get("Accept"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockValidNSEResponse()))
	}))
	defer server.Close()

	p := NewWithBaseURL(server.URL)
	quote, err := p.Get("TEST")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	verifyValidMockQuote(t, quote)
}

func verifyValidMockQuote(t *testing.T, quote *provider.Quote) {
	t.Helper()
	if quote.Symbol != "TEST" {
		t.Errorf("Symbol = %s, want TEST", quote.Symbol)
	}
	if quote.CompanyName != "Test Company" {
		t.Errorf("CompanyName = %s, want Test Company", quote.CompanyName)
	}
	if quote.LastPrice != 100.50 {
		t.Errorf("LastPrice = %f, want 100.50", quote.LastPrice)
	}
	if quote.Currency != testCurrency {
		t.Errorf("Currency = %s, want INR", quote.Currency)
	}
	if quote.Exchange != config.ExchangeNSE {
		t.Errorf("Exchange = %s, want NSE", quote.Exchange)
	}
	if len(quote.Indices) != 2 {
		t.Errorf("Indices length = %d, want 2", len(quote.Indices))
	}
}

// Helper function
func contains(s, substr string) bool {
	return s != "" && substr != "" && (s == substr || len(s) >= len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			len(s) > len(substr) && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGetMultiple_Concurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()

	// Test with multiple symbols to ensure no race conditions
	symbols := []string{"TCS", "INFY", "WIPRO", "HDFCBANK", testSymbol}
	quotes, errors := p.GetMultiple(symbols)

	if len(errors) > 0 {
		t.Logf("Got %d errors (this is OK): %v", len(errors), errors)
	}

	// Should get at least some quotes
	if len(quotes) == 0 {
		t.Error("Expected at least one quote")
	}

	// Verify each quote has correct symbol
	for symbol, quote := range quotes {
		if quote.Symbol != symbol {
			t.Errorf("Quote symbol = %v, want %v", quote.Symbol, symbol)
		}
	}
}

func TestGetMultiple_ErrorAccumulation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()

	// Mix of valid and potentially invalid symbols
	symbols := []string{"TCS", "INVALID1", "INFY", "INVALID2", "WIPRO"}
	quotes, errors := p.GetMultiple(symbols)

	// Should have some quotes
	if len(quotes) < 2 {
		t.Errorf("Expected at least 2 valid quotes, got %d", len(quotes))
	}

	// Errors may or may not be present depending on NSE behavior
	t.Logf("Got %d quotes and %d errors", len(quotes), len(errors))
}

func TestProvider_Benchmarks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping benchmark test in short mode")
	}

	p := New()

	// Simple performance check
	quote, err := p.Get("TCS")
	if err != nil {
		t.Skipf("Skipping benchmark due to error: %v", err)
	}

	if quote == nil {
		t.Error("Quote should not be nil")
	}
}

func TestQuote_DataIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := New()
	quote, err := p.Get(testSymbol)
	if err != nil {
		t.Fatalf("Get(RELIANCE) error = %v", err)
	}

	// Verify data integrity
	if quote.DayHigh < quote.DayLow && quote.DayHigh > 0 && quote.DayLow > 0 {
		t.Errorf("DayHigh (%v) should be >= DayLow (%v)", quote.DayHigh, quote.DayLow)
	}

	if quote.YearHigh < quote.YearLow && quote.YearHigh > 0 && quote.YearLow > 0 {
		t.Errorf("YearHigh (%v) should be >= YearLow (%v)", quote.YearHigh, quote.YearLow)
	}

	// LastPrice should be within day range (if available)
	if quote.DayHigh > 0 && quote.DayLow > 0 && quote.LastPrice > 0 {
		if quote.LastPrice > quote.DayHigh || quote.LastPrice < quote.DayLow {
			t.Logf("Warning: LastPrice (%v) outside day range [%v, %v]",
				quote.LastPrice, quote.DayLow, quote.DayHigh)
		}
	}
}

func TestNewWithBaseURL(t *testing.T) {
	customURL := "https://custom.example.com"
	p := NewWithBaseURL(customURL)

	if p.baseURL != customURL {
		t.Errorf("baseURL = %s, want %s", p.baseURL, customURL)
	}
}

func TestGet_MalformedJSON(t *testing.T) {
	// Test with JSON that has syntax errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"info": {"symbol": "TEST", "companyName": "Test"}`)) // Missing closing brace
	}))
	defer server.Close()

	p := NewWithBaseURL(server.URL)
	_, err := p.Get("TEST")

	if err == nil {
		t.Error("Expected error for malformed JSON")
	}
}

func TestGet_PartialJSON(t *testing.T) {
	// Test with partial but valid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"info": {
				"symbol": "TEST"
			},
			"priceInfo": {
				"lastPrice": 50.0
			}
		}`))
	}))
	defer server.Close()

	p := NewWithBaseURL(server.URL)
	quote, err := p.Get("TEST")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if quote.Symbol != "TEST" {
		t.Errorf("Symbol = %s, want TEST", quote.Symbol)
	}

	if quote.LastPrice != 50.0 {
		t.Errorf("LastPrice = %f, want 50.0", quote.LastPrice)
	}

	// Other fields should be zero/empty
	if quote.CompanyName != "" {
		t.Errorf("CompanyName should be empty, got %s", quote.CompanyName)
	}
}

func TestGet_RequestHeaders(t *testing.T) {
	// Verify that correct headers are sent
	headersCaptured := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check headers. NSE's edge (Akamai) blocks non-browser clients, so
		// we mimic a browser: Accept negotiates JSON with fallbacks, a
		// realistic User-Agent, English locale, and an XHR-style marker.
		if strings.Contains(r.Header.Get("Accept"), "application/json") &&
			r.Header.Get("Accept-Language") == "en-US,en;q=0.9" &&
			r.Header.Get("User-Agent") != "" {
			headersCaptured = true
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	p := NewWithBaseURL(server.URL)
	_, _ = p.Get("TEST")

	if !headersCaptured {
		t.Error("Expected headers were not set correctly")
	}
}

func TestGet_QueryParameter(t *testing.T) {
	// Verify that symbol is passed as query parameter
	symbolCaptured := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		symbolCaptured = r.URL.Query().Get("symbol")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	p := NewWithBaseURL(server.URL)
	_, _ = p.Get("TESTSTOCK")

	if symbolCaptured != "TESTSTOCK" {
		t.Errorf("Symbol query param = %s, want TESTSTOCK", symbolCaptured)
	}
}

func TestGet_SymbolNormalizationInRequest(t *testing.T) {
	// Verify that symbol is normalized before sending
	symbolCaptured := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		symbolCaptured = r.URL.Query().Get("symbol")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	p := NewWithBaseURL(server.URL)
	_, _ = p.Get(" lowercase ")

	if symbolCaptured != "LOWERCASE" {
		t.Errorf("Symbol query param = %s, want LOWERCASE", symbolCaptured)
	}
}

// TestFlexibleStringArray tests the custom unmarshaling for pdSectorIndAll
func TestFlexibleStringArray(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected []string
	}{
		{
			name:     "Array of strings",
			json:     `["NIFTY 50", "NIFTY IT"]`,
			expected: []string{"NIFTY 50", "NIFTY IT"},
		},
		{
			name:     "Single string (ETF case) - NA filtered out",
			json:     `"NA"`,
			expected: []string{},
		},
		{
			name:     "Empty string",
			json:     `""`,
			expected: []string{},
		},
		{
			name:     "Empty array",
			json:     `[]`,
			expected: []string{},
		},
		{
			name:     "Array with NA - filtered out",
			json:     `["NIFTY 50", "NA", "NIFTY IT"]`,
			expected: []string{"NIFTY 50", "NIFTY IT"},
		},
		{
			name:     "Valid single string",
			json:     `"NIFTY 50"`,
			expected: []string{"NIFTY 50"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var arr flexibleStringArray
			err := json.Unmarshal([]byte(tt.json), &arr)
			if err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}

			if len(arr) != len(tt.expected) {
				t.Errorf("Length = %d, want %d", len(arr), len(tt.expected))
				return
			}

			for i, val := range arr {
				if val != tt.expected[i] {
					t.Errorf("arr[%d] = %s, want %s", i, val, tt.expected[i])
				}
			}
		})
	}
}

// TestGet_ETFSymbol tests fetching ETF symbols that return pdSectorIndAll as string
func TestGet_ETFSymbol(t *testing.T) {
	// Mock server that returns ETF-style response with pdSectorIndAll as string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		response := `{
			"info": {
				"symbol": "BANKBEES",
				"companyName": "Nippon India ETF Nifty Bank BeES",
				"industry": "Mutual Fund Scheme"
			},
			"metadata": {
				"pdSectorIndAll": "NA"
			},
			"priceInfo": {
				"lastPrice": 614.26,
				"change": 1.5,
				"pChange": 0.24,
				"previousClose": 612.76,
				"open": 613.0,
				"intraDayHighLow": {
					"max": 615.0,
					"min": 612.0
				},
				"weekHighLow": {
					"max": 650.0,
					"min": 550.0
				}
			},
			"preOpenMarket": {
				"totalTradedVolume": 100000,
				"totalTradedValue": 61426000
			},
			"industryInfo": {
				"macro": "Financial Services",
				"sector": "Financial Services",
				"industry": "Mutual Fund Scheme"
			}
		}`
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	p := NewWithBaseURL(server.URL)
	quote, err := p.Get("BANKBEES")

	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if quote.Symbol != "BANKBEES" {
		t.Errorf("Symbol = %s, want BANKBEES", quote.Symbol)
	}

	// Check that pdSectorIndAll string "NA" was filtered out (empty array)
	if len(quote.Indices) != 0 {
		t.Errorf("Indices length = %d, want 0 (NA should be filtered out)", len(quote.Indices))
	}

	// Market cap should be "Other" for ETFs with no indices
	if quote.Metadata["market_cap"] != "Other" {
		t.Errorf("Market cap = %s, want Other", quote.Metadata["market_cap"])
	}
}
