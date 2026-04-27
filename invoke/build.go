package invoke

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"satis/llmconfig"
	"satis/satis"
)

type ProviderSelector interface {
	Select(ctx context.Context, cfg *llmconfig.Config, preferred string) (string, error)
}

type DefaultProviderSelector struct{}

func (s *DefaultProviderSelector) Select(_ context.Context, cfg *llmconfig.Config, preferred string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("invoke config is not configured")
	}
	if preferred = strings.TrimSpace(preferred); preferred != "" {
		if _, ok := cfg.Providers[preferred]; !ok {
			return "", fmt.Errorf("unknown invoke provider %q", preferred)
		}
		return preferred, nil
	}
	name := cfg.DefaultProvider()
	if name == "" {
		return "", fmt.Errorf("default invoke provider is not configured")
	}
	if _, ok := cfg.Providers[name]; !ok {
		return "", fmt.Errorf("default invoke provider %q is not configured", name)
	}
	return name, nil
}

type RouterInvoker struct {
	mu        sync.RWMutex
	cfg       *llmconfig.Config
	providers map[string]satis.Invoker
	selector  ProviderSelector
}

func NewRouterInvoker(cfg *llmconfig.Config) (*RouterInvoker, error) {
	router := &RouterInvoker{
		selector: &DefaultProviderSelector{},
	}
	if err := router.ReloadInvokeConfig(cfg); err != nil {
		return nil, err
	}
	return router, nil
}

func (r *RouterInvoker) ReloadInvokeConfig(cfg *llmconfig.Config) error {
	if cfg == nil {
		return fmt.Errorf("invoke config is nil")
	}
	cloned := cfg.Clone()
	providers := make(map[string]satis.Invoker, len(cloned.Providers))
	for name, providerCfg := range cloned.Providers {
		inv, err := NewOpenAICompatible(&providerCfg)
		if err != nil {
			return fmt.Errorf("provider %q: %w", name, err)
		}
		providers[name] = inv
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cloned
	r.providers = providers
	return nil
}

func (r *RouterInvoker) Invoke(ctx context.Context, prompt string, input string) (string, error) {
	return r.InvokeWithProvider(ctx, "", prompt, input)
}

func (r *RouterInvoker) InvokeWithProvider(ctx context.Context, provider string, prompt string, input string) (string, error) {
	inv, _, err := r.selectInvoker(ctx, provider)
	if err != nil {
		return "", err
	}
	return inv.Invoke(ctx, prompt, input)
}

func (r *RouterInvoker) InvokeStream(ctx context.Context, prompt string, input string, w io.Writer) (string, error) {
	return r.InvokeStreamWithProvider(ctx, "", prompt, input, w)
}

func (r *RouterInvoker) InvokeStreamWithProvider(ctx context.Context, provider string, prompt string, input string, w io.Writer) (string, error) {
	inv, _, err := r.selectInvoker(ctx, provider)
	if err != nil {
		return "", err
	}
	si, ok := inv.(satis.StreamingInvoker)
	if !ok {
		return inv.Invoke(ctx, prompt, input)
	}
	return si.InvokeStream(ctx, prompt, input, w)
}

func (r *RouterInvoker) InvokeMessages(ctx context.Context, messages []satis.ConversationMessage) (string, error) {
	return r.InvokeMessagesWithProvider(ctx, "", messages)
}

func (r *RouterInvoker) InvokeMessagesWithProvider(ctx context.Context, provider string, messages []satis.ConversationMessage) (string, error) {
	inv, _, err := r.selectInvoker(ctx, provider)
	if err != nil {
		return "", err
	}
	ci, ok := inv.(satis.ConversationInvoker)
	if !ok {
		return "", fmt.Errorf("provider does not support invoke conversations")
	}
	return ci.InvokeMessages(ctx, messages)
}

func (r *RouterInvoker) ConfigSnapshot() *llmconfig.Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.cfg == nil {
		return &llmconfig.Config{Providers: map[string]llmconfig.ProviderConfig{}}
	}
	return r.cfg.Clone()
}

func (r *RouterInvoker) selectInvoker(ctx context.Context, preferred string) (satis.Invoker, string, error) {
	r.mu.RLock()
	cfg := r.cfg
	selector := r.selector
	r.mu.RUnlock()
	name, err := selector.Select(ctx, cfg, preferred)
	if err != nil {
		return nil, "", err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	inv, ok := r.providers[name]
	if !ok {
		return nil, "", fmt.Errorf("invoke provider %q is not initialized", name)
	}
	return inv, name, nil
}

// InvokerForMode returns an Invoker for CLI/daemon flags.
// Modes: error, echo, prompt-echo, openai (requires settings when mode is openai).
func InvokerForMode(mode string, settings *Config) (satis.Invoker, error) {
	switch mode {
	case "echo":
		return echoInvoker{}, nil
	case "prompt-echo":
		return promptEchoInvoker{}, nil
	case "openai":
		if settings == nil {
			return nil, fmt.Errorf("invoke mode openai requires invoke settings (embed \"invoke\" in the VFS config file or use --invoke-config)")
		}
		return NewRouterInvoker(settings)
	case "error", "":
		return errorInvoker{}, nil
	default:
		return nil, fmt.Errorf("unknown invoke mode %q", mode)
	}
}
