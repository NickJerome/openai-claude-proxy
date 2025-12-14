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
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// 请求计数器，用于追踪请求
var requestCounter uint64

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
	// 生成请求 ID
	reqID := atomic.AddUint64(&requestCounter, 1)
	log.Printf("\n========== [REQ#%d] NEW REQUEST ==========", reqID)
	
	// 从请求头提取 API Key
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		log.Printf("[REQ#%d][ERROR] Missing Authorization header", reqID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
		return
	}

	// 提取 Bearer token
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")
	if apiKey == authHeader {
		log.Printf("[REQ#%d][ERROR] Invalid Authorization header format", reqID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization header format, expected: Bearer <token>"})
		return
	}

	log.Printf("[REQ#%d] API Key: %s...%s", reqID, apiKey[:min(10, len(apiKey))], apiKey[max(0, len(apiKey)-10):])

	// 读取原始请求体以便记录
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("[REQ#%d][ERROR] Failed to read request body: %v", reqID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(rawBody))
	
	log.Printf("[REQ#%d] ========== RAW OpenAI REQUEST ==========", reqID)
	log.Printf("%s", string(rawBody))
	log.Printf("[REQ#%d] ========== END RAW REQUEST ==========", reqID)

	// 解析 OpenAI 请求
	var openaiReq OpenAIRequest
	if err := json.Unmarshal(rawBody, &openaiReq); err != nil {
		log.Printf("[REQ#%d][ERROR] Failed to parse request: %v", reqID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[REQ#%d] OpenAI Request Summary:", reqID)
	log.Printf("[REQ#%d]   Model: %s", reqID, openaiReq.Model)
	log.Printf("[REQ#%d]   Stream: %v", reqID, openaiReq.Stream)
	log.Printf("[REQ#%d]   MaxTokens: %d", reqID, openaiReq.MaxTokens)
	log.Printf("[REQ#%d]   Tools: %d", reqID, len(openaiReq.Tools))
	log.Printf("[REQ#%d]   Messages: %d", reqID, len(openaiReq.Messages))
	log.Printf("[REQ#%d]   User (session hint): '%s'", reqID, openaiReq.User) // 关键：Cursor 传的用户/会话标识
	
	// 详细记录每条消息
	for i, msg := range openaiReq.Messages {
		contentStr := ""
		if str, ok := msg.Content.(string); ok {
			if len(str) > 500 {
				contentStr = str[:500] + "..."
			} else {
				contentStr = str
			}
		} else {
			contentBytes, _ := json.Marshal(msg.Content)
			if len(contentBytes) > 500 {
				contentStr = string(contentBytes[:500]) + "..."
			} else {
				contentStr = string(contentBytes)
			}
		}
		log.Printf("[REQ#%d]   Message[%d]: role=%s, tool_calls=%d, tool_call_id=%s", 
			reqID, i, msg.Role, len(msg.ToolCalls), msg.ToolCallID)
		log.Printf("[REQ#%d]     Content: %s", reqID, contentStr)
		
		// 详细记录 tool_calls
		for j, tc := range msg.ToolCalls {
			log.Printf("[REQ#%d]     ToolCall[%d]: id=%s, name=%s, args=%s", 
				reqID, j, tc.ID, tc.Function.Name, tc.Function.Arguments)
		}
	}

	// 应用模型映射
	originalModel := openaiReq.Model
	if mappedModel, ok := h.modelMapping[openaiReq.Model]; ok {
		openaiReq.Model = mappedModel
		log.Printf("[REQ#%d] Model mapped: %s -> %s", reqID, originalModel, mappedModel)
	}

	// 转换为 Anthropic 格式
	anthropicReq, err := ConvertOpenAIToAnthropic(openaiReq, h.maxTokensMapping, apiKey)
	if err != nil {
		log.Printf("[REQ#%d][ERROR] Conversion failed: %v", reqID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[REQ#%d] Anthropic Request Summary:", reqID)
	log.Printf("[REQ#%d]   Model: %s", reqID, anthropicReq.Model)
	log.Printf("[REQ#%d]   MaxTokens: %d", reqID, anthropicReq.MaxTokens)
	log.Printf("[REQ#%d]   System blocks: %d", reqID, len(anthropicReq.System))
	log.Printf("[REQ#%d]   Tools: %d", reqID, len(anthropicReq.Tools))
	log.Printf("[REQ#%d]   Messages: %d", reqID, len(anthropicReq.Messages))
	if anthropicReq.Metadata != nil {
		log.Printf("[REQ#%d]   Metadata.user_id: %s", reqID, anthropicReq.Metadata.UserID)
	}
	
	// 详细记录转换后的每条消息
	for i, msg := range anthropicReq.Messages {
		contentStr := ""
		if str, ok := msg.Content.(string); ok {
			if len(str) > 500 {
				contentStr = str[:500] + "..."
			} else {
				contentStr = str
			}
		} else {
			contentBytes, _ := json.Marshal(msg.Content)
			if len(contentBytes) > 500 {
				contentStr = string(contentBytes[:500]) + "..."
			} else {
				contentStr = string(contentBytes)
			}
		}
		log.Printf("[REQ#%d]   AnthropicMsg[%d]: role=%s, content=%s", reqID, i, msg.Role, contentStr)
	}

	// 序列化请求
	reqBody, err := json.Marshal(anthropicReq)
	if err != nil {
		log.Printf("[REQ#%d][ERROR] Marshal failed: %v", reqID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[REQ#%d] ========== ANTHROPIC REQUEST BODY ==========", reqID)
	log.Printf("%s", string(reqBody))
	log.Printf("[REQ#%d] ========== END ANTHROPIC REQUEST ==========", reqID)

	// 创建 HTTP 请求
	httpReq, err := http.NewRequest("POST", h.anthropicURL+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		log.Printf("[REQ#%d][ERROR] Create request failed: %v", reqID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 设置请求头 - 使用调用者提供的 API Key
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	log.Printf("[REQ#%d] Sending request to: %s/v1/messages", reqID, h.anthropicURL)

	// 发送请求
	client := &http.Client{}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("[REQ#%d][ERROR] Request failed: %v", reqID, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer httpResp.Body.Close()

	log.Printf("[REQ#%d] Anthropic response status: %d", reqID, httpResp.StatusCode)

	// 处理错误响应
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		log.Printf("[REQ#%d][ERROR] Anthropic error response: %s", reqID, string(body))
		c.JSON(httpResp.StatusCode, gin.H{
			"error": string(body),
		})
		return
	}

	// 流式响应
	if openaiReq.Stream {
		log.Printf("[REQ#%d] Handling streaming response", reqID)
		h.handleStreamResponse(c, httpResp, openaiReq.Model, reqID)
	} else {
		log.Printf("[REQ#%d] Handling non-streaming response", reqID)
		h.handleNonStreamResponse(c, httpResp, reqID)
	}
	
	log.Printf("[REQ#%d] ========== REQUEST COMPLETED ==========\n", reqID)
}

func (h *ProxyHandler) handleNonStreamResponse(c *gin.Context, httpResp *http.Response, reqID uint64) {
	// 读取完整响应以便记录
	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Printf("[REQ#%d][ERROR] Read response body failed: %v", reqID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[REQ#%d] ========== ANTHROPIC RESPONSE BODY ==========", reqID)
	log.Printf("%s", string(bodyBytes))
	log.Printf("[REQ#%d] ========== END ANTHROPIC RESPONSE ==========", reqID)

	var anthropicResp AnthropicResponse
	if err := json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		log.Printf("[REQ#%d][ERROR] Parse Anthropic response failed: %v", reqID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[REQ#%d] Anthropic Response Summary:", reqID)
	log.Printf("[REQ#%d]   ID: %s", reqID, anthropicResp.ID)
	log.Printf("[REQ#%d]   Role: %s", reqID, anthropicResp.Role)
	log.Printf("[REQ#%d]   StopReason: %s", reqID, anthropicResp.StopReason)
	log.Printf("[REQ#%d]   Content blocks: %d", reqID, len(anthropicResp.Content))
	log.Printf("[REQ#%d]   Usage: input=%d, output=%d, cache_read=%d, cache_creation=%d", reqID,
		anthropicResp.Usage.InputTokens, anthropicResp.Usage.OutputTokens,
		anthropicResp.Usage.CacheReadInputTokens, anthropicResp.Usage.CacheCreationInputTokens)

	// 转换为 OpenAI 格式
	openaiResp := ConvertAnthropicToOpenAI(anthropicResp)

	respJSON, _ := json.Marshal(openaiResp)
	log.Printf("[REQ#%d] ========== OPENAI RESPONSE BODY ==========", reqID)
	log.Printf("%s", string(respJSON))
	log.Printf("[REQ#%d] ========== END OPENAI RESPONSE ==========", reqID)

	c.JSON(http.StatusOK, openaiResp)
}

func (h *ProxyHandler) handleStreamResponse(c *gin.Context, httpResp *http.Response, model string, reqID uint64) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("[REQ#%d][ERROR] Streaming not supported by client", reqID)
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

	log.Printf("[REQ#%d] ========== STREAMING EVENTS ==========", reqID)

	for scanner.Scan() {
		line := scanner.Text()
		eventCount++

		// 记录所有事件（流式日志）
		log.Printf("[REQ#%d] Stream[%d]: %s", reqID, eventCount, line)

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
			log.Printf("[REQ#%d][WARN] Failed to parse event: %v, data: %s", reqID, err, data)
			continue
		}

		eventType, _ := event["type"].(string)
		log.Printf("[REQ#%d] EventType: %s", reqID, eventType)

		switch eventType {
		case "message_start":
			if msg, ok := event["message"].(map[string]interface{}); ok {
				messageID, _ = msg["id"].(string)
				log.Printf("[REQ#%d] Stream started - Message ID: %s", reqID, messageID)
				if u, ok := msg["usage"].(map[string]interface{}); ok {
					usage = parseUsage(u)
					log.Printf("[REQ#%d] Initial usage: input=%d, cache_creation=%d, cache_read=%d", reqID,
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
					log.Printf("[REQ#%d] Tool use started - ID: %s, Name: %s, Index: %d", reqID, toolID, toolName, toolIndex)

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
			log.Printf("[REQ#%d] Content block %d stopped", reqID, toolIndex)
			toolIndex++

		case "message_delta":
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if stopReason, ok := delta["stop_reason"].(string); ok {
					log.Printf("[REQ#%d] Stream ended - Stop reason: %s", reqID, stopReason)

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
		log.Printf("[REQ#%d][ERROR] Scanner error: %v", reqID, err)
	}

	// 发送 [DONE]
	log.Printf("[REQ#%d] ========== END STREAMING (total events: %d) ==========", reqID, eventCount)
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
