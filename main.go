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

	// 创建 Gin 路由
	r := gin.Default()

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "ok",
			"service": "OpenAI to Anthropic Proxy",
		})
	})

	// 创建代理处理器（不需要预配置 API Key）
	handler := NewProxyHandler(anthropicURL)

	// OpenAI 兼容的端点
	r.POST("/v1/chat/completions", handler.HandleChatCompletions)

	// 启动服务器
	log.Printf("Starting proxy server on port %s", port)
	log.Printf("Anthropic API URL: %s", anthropicURL)
	log.Printf("Cache control: Enabled (1h TTL)")
	log.Printf("API Key: From request Authorization header")

	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
