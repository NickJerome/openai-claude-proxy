package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ProxyHandler struct {
	anthropicURL      string
	modelMapping      map[string]string
	maxTokensMapping  map[string]int
}

func NewProxyHandler(baseURL string, modelMapping map[string]string, maxTokensMapping map[string]int) *ProxyHandler {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &ProxyHandler{
		anthropicURL:     baseURL,
		modelMapping:     modelMapping,
		maxTokensMapping: maxTokensMapping,
	}
}

func (h *ProxyHandler) HandleChatCompletions(c *gin.Context) {
	// 从请求头提取 API Key
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		log.Println("[ERROR] Missing Authorization header")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
		return
	}

	// 提取 Bearer token
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")
	if apiKey == authHeader {
		log.Println("[ERROR] Invalid Authorization header format")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization header format, expected: Bearer <token>"})
		return
	}

	log.Printf("[INFO] API Key: %s...%s", apiKey[:min(10, len(apiKey))], apiKey[max(0, len(apiKey)-10):])

	// 解析 OpenAI 请求
	var openaiReq OpenAIRequest
	if err := c.ShouldBindJSON(&openaiReq); err != nil {
		log.Printf("[ERROR] Failed to parse request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[INFO] Received OpenAI request - Model: %s, Messages: %d, Stream: %v",
		openaiReq.Model, len(openaiReq.Messages), openaiReq.Stream)

	// 应用模型映射
	originalModel := openaiReq.Model
	if mappedModel, ok := h.modelMapping[openaiReq.Model]; ok {
		openaiReq.Model = mappedModel
		log.Printf("[INFO] Model mapped: %s -> %s", originalModel, mappedModel)
	}

	// 转换为 Anthropic 格式
	anthropicReq, err := ConvertOpenAIToAnthropic(openaiReq, h.maxTokensMapping, apiKey)
	if err != nil {
		log.Printf("[ERROR] Conversion failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[INFO] Converted to Anthropic - Model: %s, Messages: %d, System blocks: %d, Tools: %d",
		anthropicReq.Model, len(anthropicReq.Messages), len(anthropicReq.System), len(anthropicReq.Tools))

	// 序列化请求
	reqBody, err := json.Marshal(anthropicReq)
	if err != nil {
		log.Printf("[ERROR] Marshal failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[DEBUG] Anthropic request body:\n%s", string(reqBody))

	// 创建 HTTP 请求
	httpReq, err := http.NewRequest("POST", h.anthropicURL+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		log.Printf("[ERROR] Create request failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 设置请求头 - 使用调用者提供的 API Key
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	log.Printf("[INFO] Sending request to: %s/v1/messages", h.anthropicURL)

	// 发送请求
	client := &http.Client{}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("[ERROR] Request failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer httpResp.Body.Close()

	log.Printf("[INFO] Anthropic response status: %d", httpResp.StatusCode)

	// 处理错误响应
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		log.Printf("[ERROR] Anthropic error response: %s", string(body))
		c.JSON(httpResp.StatusCode, gin.H{
			"error": string(body),
		})
		return
	}

	// 流式响应
	if openaiReq.Stream {
		log.Println("[INFO] Handling streaming response")
		h.handleStreamResponse(c, httpResp, openaiReq.Model)
	} else {
		log.Println("[INFO] Handling non-streaming response")
		h.handleNonStreamResponse(c, httpResp)
	}
}

func (h *ProxyHandler) handleNonStreamResponse(c *gin.Context, httpResp *http.Response) {
	// 读取完整响应以便记录
	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Printf("[ERROR] Read response body failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[DEBUG] Anthropic response body:\n%s", string(bodyBytes))

	var anthropicResp AnthropicResponse
	if err := json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		log.Printf("[ERROR] Parse Anthropic response failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[INFO] Anthropic response - ID: %s, Content blocks: %d, Stop: %s, Usage: input=%d, output=%d, cache_read=%d, cache_creation=%d",
		anthropicResp.ID, len(anthropicResp.Content), anthropicResp.StopReason,
		anthropicResp.Usage.InputTokens, anthropicResp.Usage.OutputTokens,
		anthropicResp.Usage.CacheReadInputTokens, anthropicResp.Usage.CacheCreationInputTokens)

	// 转换为 OpenAI 格式
	openaiResp := ConvertAnthropicToOpenAI(anthropicResp)

	log.Printf("[INFO] Converted to OpenAI - ID: %s, Choices: %d",
		openaiResp.ID, len(openaiResp.Choices))

	respJSON, _ := json.Marshal(openaiResp)
	log.Printf("[DEBUG] OpenAI response body:\n%s", string(respJSON))

	c.JSON(http.StatusOK, openaiResp)
}

func (h *ProxyHandler) handleStreamResponse(c *gin.Context, httpResp *http.Response, model string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Println("[ERROR] Streaming not supported by client")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	scanner := bufio.NewScanner(httpResp.Body)
	var (
		messageID   string
		usage       *AnthropicUsage
		eventCount  int
		toolIndex   int
	)

	for scanner.Scan() {
		line := scanner.Text()
		eventCount++

		if eventCount <= 10 {
			log.Printf("[DEBUG] Stream line %d: %s", eventCount, line)
		}

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data) // 去除可能的前后空格
		if data == "[DONE]" || data == "" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			log.Printf("[WARN] Failed to parse event: %v, data: %s", err, data)
			continue
		}

		eventType, _ := event["type"].(string)
		if eventCount <= 20 {
			log.Printf("[DEBUG] Event type: %s", eventType)
		}

		switch eventType {
		case "message_start":
			if msg, ok := event["message"].(map[string]interface{}); ok {
				messageID, _ = msg["id"].(string)
				log.Printf("[INFO] Stream started - Message ID: %s", messageID)
				if u, ok := msg["usage"].(map[string]interface{}); ok {
					usage = parseUsage(u)
					log.Printf("[INFO] Initial usage: input=%d, cache_creation=%d, cache_read=%d",
						usage.InputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens)
				}

				// 发送初始块（带 role）
				chunk := map[string]interface{}{
					"id":      messageID,
					"object":  "chat.completion.chunk",
					"created": getCurrentTimestamp(),
					"model":   model,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"role":    "assistant",
								"content": "",
							},
							"finish_reason": nil,
						},
					},
				}
				sendSSE(c, chunk, flusher)
			}

		case "content_block_start":
			// 处理工具调用开始
			if block, ok := event["content_block"].(map[string]interface{}); ok {
				blockType, _ := block["type"].(string)
				if blockType == "tool_use" {
					toolID, _ := block["id"].(string)
					toolName, _ := block["name"].(string)
					log.Printf("[INFO] Tool use started - ID: %s, Name: %s, Index: %d", toolID, toolName, toolIndex)

					// 发送工具调用开始事件
					chunk := map[string]interface{}{
						"id":      messageID,
						"object":  "chat.completion.chunk",
						"created": getCurrentTimestamp(),
						"model":   model,
						"choices": []map[string]interface{}{
							{
								"index": 0,
								"delta": map[string]interface{}{
									"tool_calls": []map[string]interface{}{
										{
											"index": toolIndex,
											"id":    toolID,
											"type":  "function",
											"function": map[string]string{
												"name":      toolName,
												"arguments": "",
											},
										},
									},
								},
								"finish_reason": nil,
							},
						},
					}
					sendSSE(c, chunk, flusher)
				}
			}

		case "content_block_delta":
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				deltaType, _ := delta["type"].(string)

				if deltaType == "text_delta" {
					// 处理文本内容
					if text, ok := delta["text"].(string); ok {
						chunk := map[string]interface{}{
							"id":      messageID,
							"object":  "chat.completion.chunk",
							"created": getCurrentTimestamp(),
							"model":   model,
							"choices": []map[string]interface{}{
								{
									"index": 0,
									"delta": map[string]interface{}{
										"content": text,
									},
									"finish_reason": nil,
								},
							},
						}
						sendSSE(c, chunk, flusher)
					}
				} else if deltaType == "input_json_delta" {
					// 处理工具参数增量
					if partialJSON, ok := delta["partial_json"].(string); ok {
						chunk := map[string]interface{}{
							"id":      messageID,
							"object":  "chat.completion.chunk",
							"created": getCurrentTimestamp(),
							"model":   model,
							"choices": []map[string]interface{}{
								{
									"index": 0,
									"delta": map[string]interface{}{
										"tool_calls": []map[string]interface{}{
											{
												"index": toolIndex,
												"function": map[string]string{
													"arguments": partialJSON,
												},
											},
										},
									},
									"finish_reason": nil,
								},
							},
						}
						sendSSE(c, chunk, flusher)
					}
				}
			}

		case "content_block_stop":
			// 工具块结束
			if eventCount <= 20 {
				log.Printf("[DEBUG] Content block %d stopped", toolIndex)
			}
			toolIndex++

		case "message_delta":
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if stopReason, ok := delta["stop_reason"].(string); ok {
					log.Printf("[INFO] Stream ended - Stop reason: %s", stopReason)

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
							"prompt_tokens":     usage.InputTokens,
							"completion_tokens": usage.OutputTokens,
							"total_tokens":      usage.InputTokens + usage.OutputTokens,
							"prompt_tokens_details": map[string]interface{}{
								"cached_tokens": usage.CacheReadInputTokens,
								"audio_tokens":  0,
							},
							"completion_tokens_details": map[string]interface{}{
								"reasoning_tokens":            0,
								"audio_tokens":                0,
								"accepted_prediction_tokens":  0,
								"rejected_prediction_tokens":  0,
							},
						}
					}

					sendSSE(c, chunk, flusher)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[ERROR] Scanner error: %v", err)
	}

	// 发送 [DONE]
	log.Printf("[INFO] Sending [DONE], total events: %d", eventCount)
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
