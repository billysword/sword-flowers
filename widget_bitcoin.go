package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

type bitcoinWidget struct{}

func (b *bitcoinWidget) ToolName() string { return "bitcoin_price" }

func (b *bitcoinWidget) ToolDef() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name: "bitcoin_price",
		Description: anthropic.String(
			"Show the current Bitcoin spot price in USD as an inline widget. " +
				"Use this whenever the user asks about the Bitcoin price or how much BTC costs. " +
				"Call this tool instead of describing the price in text.",
		),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{},
		},
	}
}

type coinGeckoResp struct {
	Bitcoin struct {
		USD float64 `json:"usd"`
	} `json:"bitcoin"`
}

func (b *bitcoinWidget) Render(ctx context.Context, _ json.RawMessage) (template.HTML, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd", nil)
	if err != nil {
		return "", fmt.Errorf("bitcoin widget: build request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("bitcoin widget: fetch: %w", err)
	}
	defer resp.Body.Close()

	var data coinGeckoResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("bitcoin widget: decode: %w", err)
	}

	price := data.Bitcoin.USD
	html := fmt.Sprintf(
		`<div class="msg-widget widget-btc"><span class="widget-label">BTC / USD</span><span class="widget-price">$%s</span></div>`,
		formatPrice(price),
	)
	return template.HTML(html), nil
}

// formatPrice formats a float as a comma-separated dollar amount with cents.
func formatPrice(f float64) string {
	cents := int64(f * 100)
	dollars := cents / 100
	frac := cents % 100

	// Insert commas into the dollar portion.
	s := fmt.Sprintf("%d", dollars)
	if len(s) > 3 {
		result := make([]byte, 0, len(s)+(len(s)-1)/3)
		for i, c := range s {
			if i > 0 && (len(s)-i)%3 == 0 {
				result = append(result, ',')
			}
			result = append(result, byte(c))
		}
		s = string(result)
	}
	return fmt.Sprintf("%s.%02d", s, frac)
}
