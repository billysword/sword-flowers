package main

import (
	"context"
	"encoding/json"
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
	"bitcoin_price": &bitcoinWidget{},
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
