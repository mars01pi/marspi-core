package llm

import (
	"encoding/json"
	"fmt"
	"time"
)

// RequestOptions 控制单次模型调用。零值保留原先的 300 秒、3 次重试。
// Timeout 覆盖一次调用的全部尝试和退避；MaxRetries 指向 0 时明确禁用重试。
// 流式失败后的非流式回退同样消耗一次重试预算。
type RequestOptions struct {
	Timeout    time.Duration
	MaxRetries *int
}

func (o RequestOptions) Limits() (time.Duration, int) {
	timeout, retries := o.Timeout, 3
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	if o.MaxRetries != nil {
		retries = max(0, *o.MaxRetries)
	}
	return timeout, retries
}

// ProviderOptions 的 Protocol 为空或 auto 时沿用按模型名选择的逻辑。
// Parameters 覆盖协议默认参数；值为 nil 表示删除该参数。
// model/messages/tools/stream 由运行时维护，不允许覆盖。
type ProviderOptions struct {
	Protocol   string
	Parameters map[string]any
}

// NewProviderWithOptions 允许上层显式使用通用协议并设置模型参数。
// 参数必须为 JSON 值；构造时复制，后续修改原 map 不影响请求。
func NewProviderWithOptions(model, apiURL, apiKey string, opts ProviderOptions) (Provider, error) {
	var provider Provider
	base := BaseProvider{model: model, apiURL: normalizeURL(apiURL), apiKey: apiKey}
	switch opts.Protocol {
	case "", "auto":
		provider = NewProvider(model, apiURL, apiKey)
	case "openai":
		provider = &OpenAIProvider{base}
	case "deepseek":
		provider = &DeepSeekProvider{OpenAIProvider{base}}
	case "minimax":
		base.reasoningField = "reasoning_details"
		provider = &MiniMaxProvider{OpenAIProvider{base}}
	default:
		return nil, fmt.Errorf("unsupported provider protocol: %s", opts.Protocol)
	}
	for _, key := range []string{"model", "messages", "tools", "stream"} {
		if _, ok := opts.Parameters[key]; ok {
			return nil, fmt.Errorf("reserved model parameter: %s", key)
		}
	}
	raw, err := json.Marshal(opts.Parameters)
	if err != nil {
		return nil, fmt.Errorf("invalid model parameters: %w", err)
	}
	var parameters map[string]any
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return nil, err
	}
	return &configuredProvider{Provider: provider, parameters: parameters}, nil
}

type configuredProvider struct {
	Provider
	parameters map[string]any
}

func (p *configuredProvider) BuildBody(messages []Message, tools []map[string]any) map[string]any {
	body := p.Provider.BuildBody(messages, tools)
	// 每次构造独立参数，避免调用方或传输层修改后影响后续请求。
	raw, _ := json.Marshal(p.parameters)
	var parameters map[string]any
	_ = json.Unmarshal(raw, &parameters)
	for key, value := range parameters {
		if value == nil {
			delete(body, key)
		} else {
			body[key] = value
		}
	}
	return body
}
