package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ProxyHandler struct {
	anthropicURL string
}

func NewProxyHandler(baseURL string) *ProxyHandler {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &ProxyHandler{
		anthropicURL: baseURL,
	}
}

func (h *ProxyHandler) HandleChatCompletions(c *gin.Context) {
	// 从请求头提取 API Key
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
		return
	}

	// 提取 Bearer token
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")
	if apiKey == authHeader {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization header format, expected: Bearer <token>"})
		return
	}

	// 解析 OpenAI 请求
	var openaiReq OpenAIRequest
	if err := c.ShouldBindJSON(&openaiReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 转换为 Anthropic 格式
	anthropicReq, err := ConvertOpenAIToAnthropic(openaiReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 序列化请求
	reqBody, err := json.Marshal(anthropicReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 创建 HTTP 请求
	httpReq, err := http.NewRequest("POST", h.anthropicURL+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 设置请求头 - 使用调用者提供的 API Key
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	// 发送请求
	client := &http.Client{}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer httpResp.Body.Close()

	// 处理错误响应
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		c.JSON(httpResp.StatusCode, gin.H{
			"error": string(body),
		})
		return
	}

	// 流式响应
	if openaiReq.Stream {
		h.handleStreamResponse(c, httpResp, openaiReq.Model)
	} else {
		h.handleNonStreamResponse(c, httpResp)
	}
}

func (h *ProxyHandler) handleNonStreamResponse(c *gin.Context, httpResp *http.Response) {
	var anthropicResp AnthropicResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&anthropicResp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 转换为 OpenAI 格式
	openaiResp := ConvertAnthropicToOpenAI(anthropicResp)

	c.JSON(http.StatusOK, openaiResp)
}

func (h *ProxyHandler) handleStreamResponse(c *gin.Context, httpResp *http.Response, model string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	scanner := bufio.NewScanner(httpResp.Body)
	var (
		messageID    string
		currentText  strings.Builder
		currentTools []ToolCall
		usage        *AnthropicUsage
	)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" || data == "" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "message_start":
			if msg, ok := event["message"].(map[string]interface{}); ok {
				messageID, _ = msg["id"].(string)
				if u, ok := msg["usage"].(map[string]interface{}); ok {
					usage = parseUsage(u)
				}
			}

		case "content_block_delta":
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if deltaType, _ := delta["type"].(string); deltaType == "text_delta" {
					if text, ok := delta["text"].(string); ok {
						currentText.WriteString(text)

						// 发送 OpenAI 流式事件
						chunk := map[string]interface{}{
							"id":      messageID,
							"object":  "chat.completion.chunk",
							"created": getCurrentTimestamp(),
							"model":   model,
							"choices": []map[string]interface{}{
								{
									"index": 0,
									"delta": map[string]string{
										"content": text,
									},
									"finish_reason": nil,
								},
							},
						}

						sendSSE(c, chunk, flusher)
					}
				}
			}

		case "message_delta":
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if stopReason, ok := delta["stop_reason"].(string); ok {
					// 发送最终块
					chunk := map[string]interface{}{
						"id":      messageID,
						"object":  "chat.completion.chunk",
						"created": getCurrentTimestamp(),
						"model":   model,
						"choices": []map[string]interface{}{
							{
								"index":         0,
								"delta":         map[string]interface{}{},
								"finish_reason": convertStopReason(stopReason),
							},
						},
					}

					if usage != nil {
						chunk["usage"] = map[string]interface{}{
							"prompt_tokens":                usage.InputTokens,
							"completion_tokens":            usage.OutputTokens,
							"total_tokens":                 usage.InputTokens + usage.OutputTokens,
							"cache_creation_input_tokens":  usage.CacheCreationInputTokens,
							"cache_read_input_tokens":      usage.CacheReadInputTokens,
						}
					}

					sendSSE(c, chunk, flusher)
				}
			}
		}
	}

	// 发送 [DONE]
	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()
}

func parseUsage(u map[string]interface{}) *AnthropicUsage {
	usage := &AnthropicUsage{}

	if v, ok := u["input_tokens"].(float64); ok {
		usage.InputTokens = int(v)
	}
	if v, ok := u["output_tokens"].(float64); ok {
		usage.OutputTokens = int(v)
	}
	if v, ok := u["cache_creation_input_tokens"].(float64); ok {
		usage.CacheCreationInputTokens = int(v)
	}
	if v, ok := u["cache_read_input_tokens"].(float64); ok {
		usage.CacheReadInputTokens = int(v)
	}

	return usage
}

func sendSSE(c *gin.Context, data interface{}, flusher http.Flusher) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
	flusher.Flush()
}
