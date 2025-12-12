FROM golang:1.21-alpine AS builder

WORKDIR /build

# 设置 Go 代理（加速国内构建）
ENV GOPROXY=https://goproxy.cn,direct

# 复制依赖文件
COPY go.mod ./
RUN go mod download

# 复制源代码
COPY *.go ./

# 构建
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o proxy .

# 运行阶段
FROM alpine:latest

WORKDIR /app

# 更新 apk 并安装 CA 证书
RUN apk update && \
    apk add --no-cache ca-certificates && \
    rm -rf /var/cache/apk/*

# 从构建阶段复制二进制文件
COPY --from=builder /build/proxy .

# 暴露端口
EXPOSE 8080

# 运行
ENTRYPOINT ["./proxy"]
