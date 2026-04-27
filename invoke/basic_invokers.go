package invoke

import (
	"context"
	"fmt"
	"io"
	"strings"

	"satis/satis"
)

type echoInvoker struct{}

func (echoInvoker) Invoke(_ context.Context, _ string, input string) (string, error) {
	return input, nil
}

func (echoInvoker) InvokeStream(_ context.Context, _ string, input string, w io.Writer) (string, error) {
	if w != nil && input != "" {
		if _, err := io.WriteString(w, input); err != nil {
			return "", err
		}
	}
	return input, nil
}

func (echoInvoker) InvokeMessages(_ context.Context, messages []satis.ConversationMessage) (string, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			continue
		}
		return messages[i].Content, nil
	}
	return "", nil
}

type promptEchoInvoker struct{}

func (promptEchoInvoker) Invoke(_ context.Context, prompt string, input string) (string, error) {
	return prompt + "\n\n" + input, nil
}

func (promptEchoInvoker) InvokeStream(_ context.Context, prompt string, input string, w io.Writer) (string, error) {
	out := prompt + "\n\n" + input
	if w != nil && out != "" {
		if _, err := io.WriteString(w, out); err != nil {
			return "", err
		}
	}
	return out, nil
}

func (promptEchoInvoker) InvokeMessages(_ context.Context, messages []satis.ConversationMessage) (string, error) {
	var systemParts []string
	var userPrompt string
	for _, message := range messages {
		switch message.Role {
		case "system":
			systemParts = append(systemParts, message.Content)
		case "user":
			userPrompt = message.Content
		}
	}
	return userPrompt + "\n\n" + strings.Join(systemParts, "\n\n"), nil
}

type errorInvoker struct{}

func (errorInvoker) Invoke(_ context.Context, _ string, _ string) (string, error) {
	return "", fmt.Errorf("invoke is not configured; use --invoke-mode echo, prompt-echo, or openai with invoke settings")
}

func (errorInvoker) InvokeStream(_ context.Context, _ string, _ string, _ io.Writer) (string, error) {
	return "", fmt.Errorf("invoke is not configured; use --invoke-mode echo, prompt-echo, or openai with invoke settings")
}

func (errorInvoker) InvokeMessages(_ context.Context, _ []satis.ConversationMessage) (string, error) {
	return "", fmt.Errorf("invoke is not configured; use --invoke-mode echo, prompt-echo, or openai with invoke settings")
}
