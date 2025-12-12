# OpenAI to Anthropic Proxy

一个轻量级的代理服务，将 OpenAI 格式的请求转换为 Anthropic Claude 格式，并自动启用 Prompt Caching。

## 功能特性

- ✅ **完整格式转换**：OpenAI Chat Completions API → Anthropic Messages API
- ✅ **自动缓存优化**：智能在合适位置添加 `cache_control`（1h TTL）
- ✅ **工具调用支持**：完整转换 OpenAI tools → Anthropic tools
- ✅ **流式响应**：支持 SSE 流式输出
- ✅ **零配置密钥**：API Key 从请求头自动提取，无需预配置

## 快速开始

### Docker 运行

```bash
docker run -d -p 8080:8080 ghcr.io/nickjerome/openai-claude-proxy:latest
```

### 使用方法

发送 OpenAI 格式的请求到代理：

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_ANTHROPIC_API_KEY" \
  -d '{
    "model": "claude-opus-4-5-20251101",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'
```

代理会自动：
1. 提取 `Authorization: Bearer xxx` 中的 API Key
2. 转换为 Anthropic 格式
3. 在 system 和历史消息上添加 `cache_control`
4. 转发到 Anthropic API
5. 将响应转换回 OpenAI 格式

## 缓存策略

代理会自动在以下位置添加 `cache_control`（1h TTL）：

1. **System 消息**：最后一个 system 块
2. **历史对话**：倒数第2条 assistant 消息（如果存在）
3. **最新消息**：最后一条消息

这样可以最大化缓存命中率，节省成本（缓存读取仅需 10% 成本）。

## 环境变量

创建 `.env` 文件：

```bash
# 可选：自定义 Anthropic API 端点
ANTHROPIC_BASE_URL=https://api.anthropic.com

# 可选：自定义端口
PORT=8080
```

## 本地开发

```bash
# 安装依赖
go mod download

# 运行
go run .

# 构建
go build -o proxy .
```

## Docker 构建

```bash
docker build -t openai-claude-proxy .
docker run -p 8080:8080 openai-claude-proxy
```

## 支持的功能

| 功能 | 支持状态 |
|------|---------|
| 基础消息转换 | ✅ |
| System 消息 | ✅ |
| 流式响应 | ✅ |
| 工具调用（Function Calling） | ✅ |
| 图片消息 | ✅ |
| 自动缓存（Prompt Caching） | ✅ (1h TTL) |
| 多轮对话 | ✅ |
| 温度/TopP 等参数 | ✅ |

## 注意事项

1. **API Key 安全**：API Key 通过请求头传递，代理不会存储
2. **缓存要求**：被缓存的内容需要 >= 1024 tokens
3. **工具定义**：需要客户端传递完整的 tools 定义

## License

MIT
