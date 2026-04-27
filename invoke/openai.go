package invoke

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"satis/satis"
	"strings"
	"time"
)

var thinkTagRe = regexp.MustCompile(`(?s)(?:<redacted_thinking>.*?</redacted_thinking>|<think>.*?</think>)`)

const defaultBaseURL = "https://api.openai.com/v1"
const defaultAPIKeyEnv = "OPENAI_API_KEY"
const defaultTimeoutSec = 120

// NewOpenAICompatible returns an Invoker that calls POST {base}/chat/completions.
// The concrete type also implements satis.StreamingInvoker for token streaming.
func NewOpenAICompatible(s *Settings) (satis.Invoker, error) {
	inv, err := newOpenAIInvoker(s)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

type openAIInvoker struct {
	s      *Settings
	client *http.Client
	base   string
	key    string
}

func newOpenAIInvoker(s *Settings) (*openAIInvoker, error) {
	if s == nil {
		return nil, fmt.Errorf("invoke: settings is nil")
	}
	if strings.TrimSpace(s.Model) == "" {
		return nil, fmt.Errorf("invoke: model is required")
	}
	base := strings.TrimSpace(s.BaseURL)
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")

	key := strings.TrimSpace(s.APIKey)
	if key == "" {
		envName := strings.TrimSpace(s.APIKeyEnv)
		if envName == "" {
			envName = defaultAPIKeyEnv
		}
		key = strings.TrimSpace(os.Getenv(envName))
	}

	timeout := time.Duration(s.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutSec * time.Second
	}

	return &openAIInvoker{
		s:      s,
		client: &http.Client{Timeout: timeout},
		base:   base,
		key:    key,
	}, nil
}

func (o *openAIInvoker) Invoke(ctx context.Context, prompt string, input string) (string, error) {
	msgs := buildMessages2(prompt, input)
	return o.invokeMessages(ctx, msgs)
}

func (o *openAIInvoker) InvokeMessages(ctx context.Context, messages []satis.ConversationMessage) (string, error) {
	msgs := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		msgs = append(msgs, chatMessage{
			Role:    strings.TrimSpace(message.Role),
			Content: message.Content,
		})
	}
	return o.invokeMessages(ctx, msgs)
}

func (o *openAIInvoker) invokeMessages(ctx context.Context, msgs []chatMessage) (string, error) {
	body := chatCompletionRequest{
		Model:       o.s.Model,
		Messages:    msgs,
		Temperature: o.s.Temperature,
		MaxTokens:   o.s.MaxTokens,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("invoke: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return extractTextResponse(respBody)
}

func (o *openAIInvoker) InvokeStream(ctx context.Context, prompt, input string, w io.Writer) (string, error) {
	if w == nil {
		w = io.Discard
	}
	msgs := buildMessages2(prompt, input)
	body := chatCompletionRequest{
		Model:       o.s.Model,
		Messages:    msgs,
		Temperature: o.s.Temperature,
		MaxTokens:   o.s.MaxTokens,
		Stream:      true,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("invoke: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	// Allow long SSE lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk streamCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return "", fmt.Errorf("invoke: decode stream chunk: %w", err)
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return "", fmt.Errorf("invoke: API error: %s", chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			piece := choice.Delta.Content
			if piece == "" {
				continue
			}
			full.WriteString(piece)
			if _, err := io.WriteString(w, piece); err != nil {
				return "", err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	out := StripThinkingTags(full.String())
	return strings.TrimSpace(out), nil
}

func buildMessages2(prompt, input string) []chatMessage {
	prompt = strings.TrimSpace(prompt)
	if strings.TrimSpace(input) == "" {
		return []chatMessage{{Role: "user", Content: prompt}}
	}
	return []chatMessage{
		{Role: "system", Content: input},
		{Role: "user", Content: prompt},
	}
}

// StripThinkingTags removes <redacted_thinking>...</redacted_thinking> blocks (used by some models for
// chain-of-thought / reasoning output). The thinking content is discarded here;
// streaming or TUI layers can capture it before calling this function.
func StripThinkingTags(s string) string {
	s = thinkTagRe.ReplaceAllString(s, "")
	s = stripTrailingThinkingBlock(s, "<redacted_thinking>")
	s = stripTrailingThinkingBlock(s, "<think>")
	s = strings.ReplaceAll(s, "</redacted_thinking>", "")
	s = strings.ReplaceAll(s, "</think>", "")
	return s
}

func stripTrailingThinkingBlock(s string, startTag string) string {
	for {
		idx := strings.Index(s, startTag)
		if idx < 0 {
			return s
		}
		s = s[:idx]
	}
}

func extractTextResponse(respBody []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return "", fmt.Errorf("invoke: decode response: %w", err)
	}
	if msg := responseErrorMessage(payload); msg != "" {
		return "", fmt.Errorf("invoke: API error: %s", msg)
	}
	if text, ok := extractChatChoicesText(payload); ok {
		trimmed := strings.TrimSpace(StripThinkingTags(text))
		if trimmed != "" {
			return trimmed, nil
		}
	}
	if text, ok := stringField(payload, "output_text"); ok {
		return strings.TrimSpace(StripThinkingTags(text)), nil
	}
	if text, ok := extractResponsesOutputText(payload); ok {
		return strings.TrimSpace(StripThinkingTags(text)), nil
	}
	if msg := truncatedReasoningOnlyMessage(payload); msg != "" {
		return "", fmt.Errorf("invoke: %s", msg)
	}
	return "", fmt.Errorf("invoke: empty choices in response: %s", truncateResponseForError(respBody))
}

func truncatedReasoningOnlyMessage(payload map[string]any) string {
	choicesVal, ok := payload["choices"]
	if !ok {
		return ""
	}
	choices, ok := choicesVal.([]any)
	if !ok || len(choices) == 0 {
		return ""
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return ""
	}
	finishReason, _ := choice["finish_reason"].(string)
	msgVal, ok := choice["message"]
	if !ok {
		return ""
	}
	msg, ok := msgVal.(map[string]any)
	if !ok {
		return ""
	}
	content, hasContent := stringField(msg, "content")
	reasoning, hasReasoning := stringField(msg, "reasoning_content")
	if hasContent && strings.TrimSpace(content) != "" {
		return ""
	}
	if !hasReasoning || strings.TrimSpace(reasoning) == "" {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(finishReason), "length") {
		return "provider returned reasoning-only output and hit finish_reason=length before producing final content; increase max_tokens or adjust provider/model settings"
	}
	return "provider returned reasoning-only output without final content"
}

func responseErrorMessage(payload map[string]any) string {
	errVal, ok := payload["error"]
	if !ok {
		return ""
	}
	errMap, ok := errVal.(map[string]any)
	if !ok {
		return ""
	}
	msg, _ := errMap["message"].(string)
	return strings.TrimSpace(msg)
}

func extractChatChoicesText(payload map[string]any) (string, bool) {
	choicesVal, ok := payload["choices"]
	if !ok {
		return "", false
	}
	choices, ok := choicesVal.([]any)
	if !ok || len(choices) == 0 {
		return "", false
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return "", false
	}
	if text, ok := stringField(choice, "text"); ok {
		return text, true
	}
	msgVal, ok := choice["message"]
	if !ok {
		return "", false
	}
	msg, ok := msgVal.(map[string]any)
	if !ok {
		return "", false
	}
	return extractMessageContent(msg)
}

func extractResponsesOutputText(payload map[string]any) (string, bool) {
	outputVal, ok := payload["output"]
	if !ok {
		return "", false
	}
	items, ok := outputVal.([]any)
	if !ok || len(items) == 0 {
		return "", false
	}
	var parts []string
	found := false
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		contentVal, ok := itemMap["content"]
		if !ok {
			continue
		}
		contentItems, ok := contentVal.([]any)
		if !ok {
			continue
		}
		for _, content := range contentItems {
			contentMap, ok := content.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := contentText(contentMap); ok {
				parts = append(parts, text)
				found = true
			}
		}
	}
	if !found {
		return "", false
	}
	return strings.Join(parts, ""), true
}

func extractMessageContent(message map[string]any) (string, bool) {
	contentVal, ok := message["content"]
	if !ok {
		return "", false
	}
	switch content := contentVal.(type) {
	case string:
		return content, true
	case []any:
		var parts []string
		found := false
		for _, item := range content {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := contentText(itemMap); ok {
				parts = append(parts, text)
				found = true
			}
		}
		if !found {
			return "", false
		}
		return strings.Join(parts, ""), true
	default:
		return "", false
	}
}

func contentText(content map[string]any) (string, bool) {
	if text, ok := stringField(content, "text"); ok {
		return text, true
	}
	textVal, ok := content["text"]
	if !ok {
		return "", false
	}
	textMap, ok := textVal.(map[string]any)
	if !ok {
		return "", false
	}
	return stringField(textMap, "value")
}

func stringField(m map[string]any, key string) (string, bool) {
	value, ok := m[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	return text, true
}

func truncateResponseForError(respBody []byte) string {
	const max = 500
	text := strings.TrimSpace(string(respBody))
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type streamCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
