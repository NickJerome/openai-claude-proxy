package main

type OpenAIRequest struct {
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	TopP        float64         `json:"top_p,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Tools       []OpenAITool    `json:"tools,omitempty"`
	ToolChoice  interface{}     `json:"tool_choice,omitempty"`
	User        string          `json:"user,omitempty"` // OpenAI 的 user 字段，用于生成 metadata.user_id
}

type OpenAIMessage struct {
	Role      string      `json:"role"`
	Content   interface{} `json:"content"` // string or []OpenAIContent
	ToolCalls []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type OpenAIContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

type OpenAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string      `json:"name"`
		Description string      `json:"description,omitempty"`
		Parameters  interface{} `json:"parameters"`
	} `json:"function"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Anthropic 请求结构
type AnthropicRequest struct {
	Model         string                  `json:"model"`
	MaxTokens     int                     `json:"max_tokens"`
	Messages      []AnthropicMessage      `json:"messages"`
	System        []AnthropicSystemBlock  `json:"system,omitempty"`
	Temperature   float64                 `json:"temperature,omitempty"`
	TopP          float64                 `json:"top_p,omitempty"`
	TopK          int                     `json:"top_k,omitempty"`
	Stream        bool                    `json:"stream,omitempty"`
	Tools         []interface{}           `json:"tools,omitempty"`
	ToolChoice    interface{}             `json:"tool_choice,omitempty"`
	Metadata      *Metadata               `json:"metadata,omitempty"` // Claude Code 需要的 metadata
}

// Metadata Claude Code 需要的元数据
type Metadata struct {
	UserID string `json:"user_id"`
}

type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []AnthropicContent
}

type AnthropicContent struct {
	Type         string                  `json:"type"`
	Text         *string                 `json:"text,omitempty"`
	ToolUseID    string                  `json:"tool_use_id,omitempty"`
	Content      interface{}             `json:"content,omitempty"` // 用于 tool_result
	ID           string                  `json:"id,omitempty"`
	Name         string                  `json:"name,omitempty"`
	Input        *map[string]interface{} `json:"input,omitempty"` // 使用指针，tool_use 时设置为非 nil
	CacheControl *CacheControl           `json:"cache_control,omitempty"`
	Source       *ImageSource            `json:"source,omitempty"`
}

type AnthropicSystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type AnthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// OpenAI 响应结构
type OpenAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string      `json:"role"`
			Content   string      `json:"content,omitempty"`
			ToolCalls []ToolCall  `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
			AudioTokens  int `json:"audio_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails struct {
			ReasoningTokens           int `json:"reasoning_tokens"`
			AudioTokens               int `json:"audio_tokens"`
			AcceptedPredictionTokens  int `json:"accepted_prediction_tokens"`
			RejectedPredictionTokens  int `json:"rejected_prediction_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
	ServiceTier string `json:"service_tier,omitempty"`
}

// Anthropic 响应结构
type AnthropicResponse struct {
	ID           string              `json:"id"`
	Type         string              `json:"type"`
	Role         string              `json:"role"`
	Content      []AnthropicContent  `json:"content"`
	Model        string              `json:"model"`
	StopReason   string              `json:"stop_reason"`
	StopSequence *string             `json:"stop_sequence"`
	Usage        AnthropicUsage      `json:"usage"`
}

type AnthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}
