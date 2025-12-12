package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// 加载环境变量
	_ = godotenv.Load()

	// 获取配置
	anthropicURL := os.Getenv("ANTHROPIC_BASE_URL")
	if anthropicURL == "" {
		anthropicURL = "https://api.anthropic.com"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// 解析模型映射配置
	modelMapping := parseModelMapping(os.Getenv("MODEL_MAPPING"))

	// 创建 Gin 路由
	r := gin.Default()

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":        "ok",
			"service":       "OpenAI to Anthropic Proxy",
			"model_mapping": modelMapping,
		})
	})

	// 创建代理处理器（不需要预配置 API Key）
	handler := NewProxyHandler(anthropicURL, modelMapping)

	// OpenAI 兼容的端点
	r.POST("/v1/chat/completions", handler.HandleChatCompletions)

	// 启动服务器
	log.Printf("Starting proxy server on port %s", port)
	log.Printf("Anthropic API URL: %s", anthropicURL)
	log.Printf("Cache control: Enabled (1h TTL)")
	log.Printf("API Key: From request Authorization header")
	if len(modelMapping) > 0 {
		log.Printf("Model mapping: %v", modelMapping)
	} else {
		log.Printf("Model mapping: Disabled (passthrough)")
	}

	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

// parseModelMapping 解析模型映射配置
// 格式: "model1:target1,model2:target2"
// 示例: "gpt-4:claude-opus-4-5-20251101,gpt-3.5-turbo:claude-3-5-haiku-20241022"
func parseModelMapping(mappingStr string) map[string]string {
	mapping := make(map[string]string)

	if mappingStr == "" {
		return mapping
	}

	pairs := strings.Split(mappingStr, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(parts) == 2 {
			source := strings.TrimSpace(parts[0])
			target := strings.TrimSpace(parts[1])
			if source != "" && target != "" {
				mapping[source] = target
			}
		}
	}

	return mapping
}
