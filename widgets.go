package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"

	"github.com/anthropics/anthropic-sdk-go"
)

// Widget is implemented by each mini-app module.
// Adding a new widget: implement this interface and register in widgetRegistry.
type Widget interface {
	ToolName() string
	ToolDef() anthropic.ToolParam
	Render(ctx context.Context, input json.RawMessage) (template.HTML, error)
}

var widgetRegistry = map[string]Widget{
	"stock_price": &stockWidget{},
}

// widgetTools returns a ToolUnionParam for each registered widget,
// ready to be appended to the MessageNewParams.Tools list.
func widgetTools() []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(widgetRegistry))
	for _, w := range widgetRegistry {
		def := w.ToolDef()
		out = append(out, anthropic.ToolUnionParam{OfTool: &def})
	}
	return out
}

// formatPrice formats a float as a comma-separated dollar amount with cents.
func formatPrice(f float64) string {
	cents := int64(f * 100)
	dollars := cents / 100
	frac := cents % 100

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
