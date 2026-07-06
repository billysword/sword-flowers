package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

type stockWidget struct{}

func (s *stockWidget) ToolName() string { return "stock_price" }

func (s *stockWidget) ToolDef() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name: "stock_price",
		Description: anthropic.String(
			"Show the current price and 5-day trend chart for any stock or cryptocurrency. " +
				"Call this for any $TICKER query. " +
				"Symbol format — stocks: GOOGL, STRC, AAPL. " +
				"Crypto: BTC-USD, ETH-USD, SOL-USD. " +
				"Always call this tool instead of stating the price in text.",
		),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"symbol": map[string]any{
					"type":        "string",
					"description": "Yahoo Finance symbol. Stocks: GOOGL, STRC. Crypto: BTC-USD, ETH-USD.",
				},
			},
			Required: []string{"symbol"},
		},
	}
}

// normaliseTicker converts bare crypto names/tickers to Yahoo Finance format.
// "BTC" → "BTC-USD", "ETH" → "ETH-USD", "GOOGL" → "GOOGL"
func normaliseTicker(sym string) string {
	sym = strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(sym), "$"))
	switch sym {
	case "BTC", "BITCOIN":
		return "BTC-USD"
	case "ETH", "ETHEREUM":
		return "ETH-USD"
	case "SOL", "SOLANA":
		return "SOL-USD"
	case "DOGE", "DOGECOIN":
		return "DOGE-USD"
	}
	return sym
}

type yahooChartResp struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol             string  `json:"symbol"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
			} `json:"meta"`
			Indicators struct {
				Quote []struct {
					Close []float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

func (s *stockWidget) Render(ctx context.Context, input json.RawMessage) (template.HTML, error) {
	var params struct {
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("stock widget: parse input: %w", err)
	}

	sym := normaliseTicker(params.Symbol)

	url := fmt.Sprintf(
		"https://query2.finance.yahoo.com/v8/finance/chart/%s?interval=1d&range=5d",
		sym,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("stock widget: build request: %w", err)
	}
	// Yahoo Finance requires a browser-like User-Agent to avoid 403s.
	req.Header.Set("User-Agent", "Mozilla/5.0")

	c := &http.Client{Timeout: 8 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("stock widget: fetch %s: %w", sym, err)
	}
	defer resp.Body.Close()

	var data yahooChartResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("stock widget: decode: %w", err)
	}

	if data.Chart.Error != nil {
		return "", fmt.Errorf("stock widget: %s", data.Chart.Error.Description)
	}
	if len(data.Chart.Result) == 0 {
		return "", fmt.Errorf("stock widget: no data for %s", sym)
	}

	result := data.Chart.Result[0]
	price := result.Meta.RegularMarketPrice
	displaySym := result.Meta.Symbol

	var spark string
	if len(result.Indicators.Quote) > 0 {
		spark = sparkline(result.Indicators.Quote[0].Close)
	}

	html := fmt.Sprintf(
		`<div class="msg-widget widget-stock"><span class="widget-symbol">%s</span><span class="widget-price">$%s</span><span class="widget-spark">%s</span></div>`,
		displaySym, formatPrice(price), spark,
	)
	return template.HTML(html), nil
}

// sparkline maps a slice of prices to Unicode block characters ▁▂▃▄▅▆▇█.
// Zero values (Yahoo Finance returns null as 0.0 for non-trading days) are skipped.
func sparkline(prices []float64) string {
	var clean []float64
	for _, p := range prices {
		if p > 0 {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return ""
	}

	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	min, max := clean[0], clean[0]
	for _, p := range clean {
		if p < min {
			min = p
		}
		if p > max {
			max = p
		}
	}

	result := make([]rune, len(clean))
	for i, p := range clean {
		idx := 0
		if max > min {
			idx = int((p - min) / (max - min) * float64(len(blocks)-1))
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		result[i] = blocks[idx]
	}
	return string(result)
}
