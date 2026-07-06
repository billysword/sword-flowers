package main

import "regexp"

// tickerRe matches a dollar-prefixed symbol anywhere in the message.
// The $ sign is the explicit user opt-in for the stock widget.
// Examples: $GOOGL, $BTC, $ETH-USD, $STRC
var tickerRe = regexp.MustCompile(`(?i)\$[A-Z][A-Z0-9\-]{0,9}`)

// routeToWidget returns the widget tool name to force if the user message
// contains a $TICKER pattern. Returns ("", false) when no match — Claude
// then decides normally via tool descriptions.
func routeToWidget(msg string) (toolName string, ok bool) {
	if tickerRe.MatchString(msg) {
		return "stock_price", true
	}
	return "", false
}
