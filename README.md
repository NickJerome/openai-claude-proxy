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

创建 `.env` 文件或在 `docker run` 时指定：

```bash
# 可选：自定义 Anthropic API 端点
ANTHROPIC_BASE_URL=https://api.anthropic.com
# 或使用第三方端点
# ANTHROPIC_BASE_URL=https://www.openclaudecode.cn

# 可选：自定义端口
PORT=8080

# 可选：模型名称映射（默认不映射，直接透传）
# 格式: "源模型:目标模型,源模型2:目标模型2"
MODEL_MAPPING=gpt-4:claude-opus-4-5-20251101,gpt-3.5-turbo:claude-3-5-haiku-20241022

# 可选：Max Tokens 映射（为每个模型单独设置 max_tokens）
# 格式: "模型1:tokens1,模型2:tokens2"
# 注意: 这里使用的是映射后的模型名（即 Anthropic 实际使用的模型名）
# 优先级: MAX_TOKENS_MAPPING > MAX_TOKENS > 内置默认值
MAX_TOKENS_MAPPING=claude-opus-4-5-20251101:16384,claude-3-5-sonnet-20241022:8192,claude-3-haiku-20240307:4096

# 可选：全局默认 Max Tokens（当请求未指定且 MAX_TOKENS_MAPPING 未匹配时使用）
# 默认值: 根据模型自动选择（opus-4: 16384, opus/sonnet: 8192, haiku: 4096, 其他: 8192）
MAX_TOKENS=8192
```

### 使用示例

**使用 OCC 第三方端点 + 模型映射 + Max Tokens 配置**：

```bash
docker run -d -p 8080:8080 \
  -e ANTHROPIC_BASE_URL=https://www.openclaudecode.cn \
  -e MODEL_MAPPING=gpt-4:claude-opus-4-5-20251101 \
  -e MAX_TOKENS_MAPPING=claude-opus-4-5-20251101:16384 \
  ghcr.io/nickjerome/openai-claude-proxy:latest
```

这样客户端请求 `gpt-4` 会自动映射到 `claude-opus-4-5-20251101`，并使用 16384 的 max_tokens。

**使用全局 MAX_TOKENS**：

```bash
docker run -d -p 8080:8080 \
  -e ANTHROPIC_BASE_URL=https://www.openclaudecode.cn \
  -e MAX_TOKENS=16384 \
  ghcr.io/nickjerome/openai-claude-proxy:latest
```

**不映射，直接透传**（默认）：

```bash
docker run -d -p 8080:8080 \
  -e ANTHROPIC_BASE_URL=https://www.openclaudecode.cn \
  ghcr.io/nickjerome/openai-claude-proxy:latest
```

客户端请求什么模型就使用什么模型。

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
