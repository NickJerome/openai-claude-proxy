package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// ConvertOpenAIToAnthropic 完全参考 new-api/relay/channel/claude/relay-claude.go:75-482
func ConvertOpenAIToAnthropic(req OpenAIRequest) (*AnthropicRequest, error) {
	// 转换工具定义
	claudeTools := make([]interface{}, 0, len(req.Tools))
	for _, tool := range req.Tools {
		if params, ok := tool.Function.Parameters.(map[string]interface{}); ok {
			claudeTool := AnthropicTool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				InputSchema: make(map[string]interface{}),
			}

			if params["type"] != nil {
				if typeStr, ok := params["type"].(string); ok {
					claudeTool.InputSchema["type"] = typeStr
				}
			}
			claudeTool.InputSchema["properties"] = params["properties"]
			claudeTool.InputSchema["required"] = params["required"]

			// 复制其他字段
			for key, val := range params {
				if key != "type" && key != "properties" && key != "required" {
					claudeTool.InputSchema[key] = val
				}
			}

			claudeTools = append(claudeTools, claudeTool)
		}
	}

	anthReq := &AnthropicRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
		Tools:       claudeTools,
	}

	if anthReq.MaxTokens == 0 {
		anthReq.MaxTokens = 4096
	}

	// 格式化消息：合并连续相同角色的消息
	formatMessages := make([]OpenAIMessage, 0)
	var lastMessage OpenAIMessage
	lastMessage.Role = "tool"

	for _, message := range req.Messages {
		if message.Role == "" {
			message.Role = "user"
		}

		// 合并连续相同角色的消息（tool 除外）
		if lastMessage.Role == message.Role && lastMessage.Role != "tool" {
			if isStringContent(lastMessage.Content) && isStringContent(message.Content) {
				// 合并文本内容
				combined := fmt.Sprintf("%s %s", getStringContent(lastMessage.Content), getStringContent(message.Content))
				message.Content = strings.Trim(combined, "\"")
				// 删除上一条消息
				formatMessages = formatMessages[:len(formatMessages)-1]
			}
		}

		// 如果 content 是 nil，设置为占位符
		if message.Content == nil {
			message.Content = "..."
		}

		formatMessages = append(formatMessages, message)
		lastMessage = message
	}

	// 转换消息
	claudeMessages := make([]AnthropicMessage, 0)
	systemMessages := make([]AnthropicSystemBlock, 0)
	isFirstMessage := true

	for _, message := range formatMessages {
		// 提取 system 消息
		if message.Role == "system" {
			if isStringContent(message.Content) {
				systemMessages = append(systemMessages, AnthropicSystemBlock{
					Type: "text",
					Text: getStringContent(message.Content),
				})
			} else if contentArray, ok := message.Content.([]interface{}); ok {
				for _, item := range contentArray {
					if contentMap, ok := item.(map[string]interface{}); ok {
						if contentType, _ := contentMap["type"].(string); contentType == "text" {
							if text, ok := contentMap["text"].(string); ok {
								systemMessages = append(systemMessages, AnthropicSystemBlock{
									Type: "text",
									Text: text,
								})
							}
						}
					}
				}
			}
			continue
		}

		// 确保第一条消息是 user
		if isFirstMessage {
			isFirstMessage = false
			if message.Role != "user" {
				log.Println("[INFO] First message is not user, adding placeholder user message")
				claudeMessages = append(claudeMessages, AnthropicMessage{
					Role: "user",
					Content: []AnthropicContent{
						{Type: "text", Text: stringPtr("...")},
					},
				})
			}
		}

		anthMsg := AnthropicMessage{
			Role: message.Role,
		}

		// 处理 tool 结果
		if message.Role == "tool" && message.ToolCallID != "" {
			toolResult := AnthropicContent{
				Type:      "tool_result",
				ToolUseID: message.ToolCallID,
				Content:   message.Content,
			}

			// 尝试合并到上一条 user 消息
			if len(claudeMessages) > 0 && claudeMessages[len(claudeMessages)-1].Role == "user" {
				lastMsg := &claudeMessages[len(claudeMessages)-1]

				// 确保 content 是数组格式
				if strContent, ok := lastMsg.Content.(string); ok {
					lastMsg.Content = []AnthropicContent{
						{Type: "text", Text: stringPtr(strContent)},
					}
				}

				if contents, ok := lastMsg.Content.([]AnthropicContent); ok {
					lastMsg.Content = append(contents, toolResult)
					log.Printf("[INFO] Merged tool_result into previous user message")
					continue
				}
			} else {
				// 创建新的 user 消息
				anthMsg.Role = "user"
				anthMsg.Content = []AnthropicContent{toolResult}
			}
		} else if isStringContent(message.Content) && len(message.ToolCalls) == 0 {
			// 纯文本消息
			anthMsg.Content = getStringContent(message.Content)
		} else {
			// 复杂内容或有 tool_calls
			anthContents := make([]AnthropicContent, 0)

			// 转换 content
			if contentArray, ok := message.Content.([]interface{}); ok {
				for _, item := range contentArray {
					contentMap, ok := item.(map[string]interface{})
					if !ok {
						continue
					}

					contentType, _ := contentMap["type"].(string)

					if contentType == "text" {
						text, _ := contentMap["text"].(string)
						if text == "" {
							log.Println("[DEBUG] Skipping empty text block")
							continue // 跳过空文本块
						}
						anthContents = append(anthContents, AnthropicContent{
							Type: "text",
							Text: stringPtr(text),
						})
					} else if contentType == "image_url" {
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
			}

			// 添加 tool_calls
			if len(message.ToolCalls) > 0 {
				for _, toolCall := range message.ToolCalls {
					var input map[string]interface{}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &input); err != nil {
						log.Printf("[WARN] Failed to parse tool call arguments: %v", err)
						continue
					}

					anthContents = append(anthContents, AnthropicContent{
						Type:  "tool_use",
						ID:    toolCall.ID,
						Name:  toolCall.Function.Name,
						Input: input,
					})
				}
			}

			if len(anthContents) > 0 {
				anthMsg.Content = anthContents
			}
		}

		claudeMessages = append(claudeMessages, anthMsg)
	}

	// 添加 system 消息并设置 cache_control
	if len(systemMessages) > 0 {
		systemMessages[len(systemMessages)-1].CacheControl = &CacheControl{
			Type: "ephemeral",
			TTL:  "1h",
		}
		log.Printf("[INFO] Added cache_control to system (1h TTL)")
		anthReq.System = systemMessages
	}

	// 在倒数第2条 assistant 消息添加 cache_control
	if len(claudeMessages) >= 2 {
		secondLast := &claudeMessages[len(claudeMessages)-2]
		if secondLast.Role == "assistant" {
			addCacheControlToMessage(secondLast)
			log.Printf("[INFO] Added cache_control to second-to-last assistant message (1h TTL)")
		}
	}

	anthReq.Messages = claudeMessages
	return anthReq, nil
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

func isStringContent(content interface{}) bool {
	_, ok := content.(string)
	return ok
}

func getStringContent(content interface{}) string {
	if str, ok := content.(string); ok {
		return str
	}
	return ""
}

func convertToolChoice(choice interface{}) interface{} {
	if choice == nil {
		return nil
	}

	switch v := choice.(type) {
	case string:
		if v == "auto" || v == "required" || v == "none" {
			return map[string]string{"type": v}
		}
	case map[string]interface{}:
		return v
	}

	return nil
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

	// 初始化 choices
	resp.Choices = make([]struct {
		Index   int `json:"index"`
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content,omitempty"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	}, 1)

	// 转换内容
	var textParts []string
	var toolCalls []ToolCall

	for _, content := range anthResp.Content {
		switch content.Type {
		case "text":
			if content.Text != nil {
				textParts = append(textParts, *content.Text)
			}
		case "tool_use":
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
	resp.Choices[0].Message.ToolCalls = toolCalls

	if len(toolCalls) > 0 {
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
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}

func getCurrentTimestamp() int64 {
	return int64(1765521600)
}
