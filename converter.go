package main

import (
	"encoding/json"
	"strings"
)

// ConvertOpenAIToAnthropic 将 OpenAI 请求转换为 Anthropic 请求
func ConvertOpenAIToAnthropic(req OpenAIRequest) (*AnthropicRequest, error) {
	anthReq := &AnthropicRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
		Messages:    make([]AnthropicMessage, 0),
		System:      make([]AnthropicSystemBlock, 0),
	}

	// 默认 max_tokens
	if anthReq.MaxTokens == 0 {
		anthReq.MaxTokens = 4096
	}

	// 转换消息
	for _, msg := range req.Messages {
		// 提取 system 消息
		if msg.Role == "system" {
			systemText := extractTextContent(msg.Content)
			if systemText != "" {
				anthReq.System = append(anthReq.System, AnthropicSystemBlock{
					Type: "text",
					Text: systemText,
				})
			}
			continue
		}

		anthMsg := AnthropicMessage{
			Role: msg.Role,
		}

		// 转换 content
		switch content := msg.Content.(type) {
		case string:
			// 字符串类型，直接使用
			if content != "" {
				anthMsg.Content = content
			}
		case []interface{}:
			// 数组类型，转换每个块
			anthContents := make([]AnthropicContent, 0)
			for _, item := range content {
				contentMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}

				contentType, _ := contentMap["type"].(string)

				if contentType == "text" {
					text, _ := contentMap["text"].(string)
					if text == "" {
						continue // 跳过空文本块
					}
					anthContents = append(anthContents, AnthropicContent{
						Type: "text",
						Text: stringPtr(text),
					})
				} else if contentType == "image_url" {
					// 处理图片
					if imageURL, ok := contentMap["image_url"].(map[string]interface{}); ok {
						url, _ := imageURL["url"].(string)
						anthContents = append(anthContents, AnthropicContent{
							Type: "image",
							Source: &ImageSource{
								Type: "url",
								URL:  url,
							},
						})
					}
				}
			}

			if len(anthContents) > 0 {
				anthMsg.Content = anthContents
			}
		}

		// 转换 tool_calls
		if len(msg.ToolCalls) > 0 {
			// 如果有 tool_calls，需要转换为数组格式
			anthContents := make([]AnthropicContent, 0)

			// 如果之前已经有 content，先添加
			if str, ok := anthMsg.Content.(string); ok && str != "" {
				anthContents = append(anthContents, AnthropicContent{
					Type: "text",
					Text: stringPtr(str),
				})
			} else if contents, ok := anthMsg.Content.([]AnthropicContent); ok {
				anthContents = append(anthContents, contents...)
			}

			// 添加 tool_use 块
			for _, toolCall := range msg.ToolCalls {
				var input map[string]interface{}
				if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &input); err != nil {
					continue
				}

				anthContents = append(anthContents, AnthropicContent{
					Type:  "tool_use",
					ID:    toolCall.ID,
					Name:  toolCall.Function.Name,
					Input: input,
				})
			}

			anthMsg.Content = anthContents
		}

		// 处理 tool 结果（role == "tool"）
		if msg.Role == "tool" && msg.ToolCallID != "" {
			// 转换为 user 消息，包含 tool_result
			toolResult := AnthropicContent{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
			}

			resultText := extractTextContent(msg.Content)
			if resultText != "" {
				toolResult.Text = stringPtr(resultText)
			}

			anthMsg.Role = "user"
			anthMsg.Content = []AnthropicContent{toolResult}
		}

		anthReq.Messages = append(anthReq.Messages, anthMsg)
	}

	// 合并连续的相同角色消息
	anthReq.Messages = mergeConsecutiveMessages(anthReq.Messages)

	// 转换工具定义
	if len(req.Tools) > 0 {
		anthReq.Tools = make([]interface{}, 0, len(req.Tools))
		for _, tool := range req.Tools {
			anthTool := AnthropicTool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
			}

			// 转换 parameters 为 input_schema
			if params, ok := tool.Function.Parameters.(map[string]interface{}); ok {
				anthTool.InputSchema = params
			}

			anthReq.Tools = append(anthReq.Tools, anthTool)
		}
	}

	// 转换 tool_choice
	if req.ToolChoice != nil {
		anthReq.ToolChoice = convertToolChoice(req.ToolChoice)
	}

	// 自动添加 cache_control
	addCacheControl(anthReq)

	return anthReq, nil
}

// addCacheControl 自动在合适的位置添加缓存控制
func addCacheControl(req *AnthropicRequest) {
	// 1. 在 system 的最后一个块添加 cache_control
	if len(req.System) > 0 {
		req.System[len(req.System)-1].CacheControl = &CacheControl{
			Type: "ephemeral",
			TTL:  "1h",
		}
	}

	// 2. 在倒数第2条 assistant 消息添加（如果存在且够长）
	if len(req.Messages) >= 2 {
		secondLast := &req.Messages[len(req.Messages)-2]
		if secondLast.Role == "assistant" {
			addCacheControlToMessage(secondLast)
		}
	}

	// 3. 在最后一条消息添加
	if len(req.Messages) > 0 {
		lastMsg := &req.Messages[len(req.Messages)-1]
		addCacheControlToMessage(lastMsg)
	}
}

func addCacheControlToMessage(msg *AnthropicMessage) {
	switch content := msg.Content.(type) {
	case []AnthropicContent:
		if len(content) > 0 {
			content[len(content)-1].CacheControl = &CacheControl{
				Type: "ephemeral",
				TTL:  "1h",
			}
			msg.Content = content
		}
	case string:
		// 转换字符串为数组格式
		if content != "" {
			msg.Content = []AnthropicContent{
				{
					Type:         "text",
					Text:         stringPtr(content),
					CacheControl: &CacheControl{Type: "ephemeral", TTL: "1h"},
				},
			}
		}
	}
}

// mergeConsecutiveMessages 合并连续的相同角色消息
func mergeConsecutiveMessages(messages []AnthropicMessage) []AnthropicMessage {
	if len(messages) <= 1 {
		return messages
	}

	merged := make([]AnthropicMessage, 0)
	current := messages[0]

	for i := 1; i < len(messages); i++ {
		next := messages[i]

		if current.Role == next.Role && current.Role != "tool" {
			// 合并内容
			currentContents := toContentArray(current.Content)
			nextContents := toContentArray(next.Content)
			current.Content = append(currentContents, nextContents...)
		} else {
			merged = append(merged, current)
			current = next
		}
	}

	merged = append(merged, current)
	return merged
}

func toContentArray(content interface{}) []AnthropicContent {
	switch c := content.(type) {
	case []AnthropicContent:
		return c
	case string:
		if c != "" {
			return []AnthropicContent{{Type: "text", Text: stringPtr(c)}}
		}
		return []AnthropicContent{}
	default:
		return []AnthropicContent{}
	}
}

func convertToolChoice(choice interface{}) interface{} {
	if choice == nil {
		return nil
	}

	switch v := choice.(type) {
	case string:
		if v == "auto" || v == "required" {
			return map[string]string{"type": v}
		}
		if v == "none" {
			return map[string]string{"type": "none"}
		}
	case map[string]interface{}:
		return v
	}

	return nil
}

func extractTextContent(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var texts []string
		for _, item := range c {
			if m, ok := item.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					texts = append(texts, t)
				}
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func stringPtr(s string) *string {
	return &s
}

// ConvertAnthropicToOpenAI 将 Anthropic 响应转换为 OpenAI 响应
func ConvertAnthropicToOpenAI(anthResp AnthropicResponse) OpenAIResponse {
	resp := OpenAIResponse{
		ID:      anthResp.ID,
		Object:  "chat.completion",
		Created: getCurrentTimestamp(),
		Model:   anthResp.Model,
		Choices: make([]struct {
			Index   int `json:"index"`
			Message struct {
				Role      string     `json:"role"`
				Content   string     `json:"content,omitempty"`
				ToolCalls []ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}, 1),
		Usage: struct {
			PromptTokens             int `json:"prompt_tokens"`
			CompletionTokens         int `json:"completion_tokens"`
			TotalTokens              int `json:"total_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
		}{
			PromptTokens:             anthResp.Usage.InputTokens,
			CompletionTokens:         anthResp.Usage.OutputTokens,
			TotalTokens:              anthResp.Usage.InputTokens + anthResp.Usage.OutputTokens,
			CacheCreationInputTokens: anthResp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     anthResp.Usage.CacheReadInputTokens,
		},
	}

	// 转换内容和工具调用
	var textParts []string
	var toolCalls []ToolCall

	for _, content := range anthResp.Content {
		if content.Type == "text" && content.Text != nil {
			textParts = append(textParts, *content.Text)
		} else if content.Type == "tool_use" {
			argsBytes, _ := json.Marshal(content.Input)
			toolCalls = append(toolCalls, ToolCall{
				ID:   content.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      content.Name,
					Arguments: string(argsBytes),
				},
			})
		}
	}

	resp.Choices[0].Message.Role = anthResp.Role
	resp.Choices[0].Message.Content = strings.Join(textParts, "")

	if len(toolCalls) > 0 {
		resp.Choices[0].Message.ToolCalls = toolCalls
		resp.Choices[0].FinishReason = "tool_calls"
	} else {
		resp.Choices[0].FinishReason = convertStopReason(anthResp.StopReason)
	}

	return resp
}

func convertStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		return reason
	}
}

func getCurrentTimestamp() int64 {
	return int64(1765521600) // 简化实现，实际应该用 time.Now().Unix()
}
