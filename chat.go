package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const chatSystemPrompt = "You are a helpful research assistant in a retro terminal interface. " +
	"You have access to web search — use it proactively when the user asks about " +
	"current events, news, prices, or anything requiring up-to-date information. " +
	"Be informative but concise. Format responses as plain text suitable for a terminal display."

type sessionData struct {
	mu      sync.Mutex
	history []anthropic.MessageParam
	display []chatMsg
}

type chatMsg struct {
	Role    string
	Content string
}

// chatStore maps session UUID strings to *sessionData.
// In-memory only; conversations reset on server restart.
var chatStore sync.Map

func newAnthropicClient(apiKey string) anthropic.Client {
	return anthropic.NewClient(option.WithAPIKey(apiKey))
}

func chatPageHandler(tmpl *templates) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// "/" is a catch-all in stdlib routing; reject anything that isn't exactly "/".
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}

		sess := getOrCreateSession(w, r)

		sess.mu.Lock()
		msgs := make([]chatMsg, len(sess.display))
		copy(msgs, sess.display)
		sess.mu.Unlock()

		data := struct{ Messages []chatMsg }{Messages: msgs}
		if err := tmpl.chat.Execute(w, data); err != nil {
			log.Printf("chat page render: %v", err)
		}
	}
}

// chatHandler handles POST /chat.
// It appends the user message to the session, calls Claude with streaming, and
// writes the response as a text/event-stream so the client can consume it with
// fetch + ReadableStream. Each token is JSON-encoded in a data: frame so that
// embedded newlines in Claude's output don't break SSE framing.
func chatHandler(client anthropic.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}

		userMsg := strings.TrimSpace(r.FormValue("message"))
		if userMsg == "" {
			http.Error(w, "message required", http.StatusBadRequest)
			return
		}

		sess := getOrCreateSession(w, r)

		sess.mu.Lock()
		sess.history = append(sess.history, anthropic.NewUserMessage(
			anthropic.NewTextBlock(userMsg),
		))
		sess.display = append(sess.display, chatMsg{Role: "user", Content: userMsg})
		history := make([]anthropic.MessageParam, len(sess.history))
		copy(history, sess.history)
		sess.mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		var fullReply strings.Builder

		// web_search_20250305 is a server-side tool: Anthropic executes searches
		// transparently. When the model decides to search, the API returns
		// stop_reason "pause_turn". We re-call with the accumulated history until
		// the model finishes (stop_reason "end_turn").
		for {
			var msg anthropic.Message
			stream := client.Messages.NewStreaming(r.Context(), anthropic.MessageNewParams{
				Model:     anthropic.ModelClaudeSonnet4_6,
				MaxTokens: 8192,
				System: []anthropic.TextBlockParam{
					{Text: chatSystemPrompt},
				},
				Tools: []anthropic.ToolUnionParam{
					{OfWebSearchTool20250305: &anthropic.WebSearchTool20250305Param{}},
				},
				Messages: history,
			})

			for stream.Next() {
				event := stream.Current()
				if err := msg.Accumulate(event); err != nil {
					log.Printf("chat accumulate: %v", err)
				}
				if cbde, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
					if td, ok := cbde.Delta.AsAny().(anthropic.TextDelta); ok {
						encoded, _ := json.Marshal(td.Text)
						fmt.Fprintf(w, "data: %s\n\n", encoded)
						flusher.Flush()
						fullReply.WriteString(td.Text)
					}
				}
			}

			if err := stream.Err(); err != nil {
				log.Printf("chat stream error: %v", err)
				fmt.Fprintf(w, "data: %s\n\n", `"[error: connection lost]"`)
				flusher.Flush()
				return
			}

			history = append(history, msg.ToParam())
			if msg.StopReason != anthropic.StopReasonPauseTurn {
				break
			}
		}

		// Save completed assistant turn to session.
		if reply := fullReply.String(); reply != "" {
			sess.mu.Lock()
			sess.history = history
			sess.display = append(sess.display, chatMsg{Role: "assistant", Content: reply})
			sess.mu.Unlock()
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

// getOrCreateSession returns the *sessionData for the current request's
// chat_session cookie, creating a new session (and setting the cookie) if absent.
func getOrCreateSession(w http.ResponseWriter, r *http.Request) *sessionData {
	const cookieName = "chat_session"

	var id string
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		id = c.Value
	} else {
		b := make([]byte, 16)
		if _, randErr := rand.Read(b); randErr != nil {
			log.Printf("crypto/rand: %v", randErr)
		}
		id = hex.EncodeToString(b)
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    id,
			Path:     "/",
			MaxAge:   86400 * 30,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}

	actual, _ := chatStore.LoadOrStore(id, &sessionData{})
	return actual.(*sessionData)
}
