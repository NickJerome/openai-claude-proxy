FROM golang:1.21-alpine AS builder

WORKDIR /build

# 设置 Go 代理（加速国内构建）
ENV GOPROXY=https://goproxy.cn,direct

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY *.go ./

# 构建
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o proxy .

# 运行阶段 - 使用 scratch 镜像避免 apk 问题
FROM scratch

WORKDIR /app

# 复制 CA 证书（从 builder 阶段）
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# 从构建阶段复制二进制文件
COPY --from=builder /build/proxy .

# 暴露端口
EXPOSE 8080

# 运行
ENTRYPOINT ["./proxy"]
