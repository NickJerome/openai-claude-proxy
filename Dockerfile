FROM golang:1.21-alpine AS builder

WORKDIR /build

# 复制依赖文件
COPY go.mod go.sum* ./
RUN go mod download

# 复制源代码
COPY *.go ./

# 构建
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o proxy .

# 运行阶段
FROM alpine:latest

WORKDIR /app

# 安装 CA 证书
RUN apk --no-cache add ca-certificates

# 从构建阶段复制二进制文件
COPY --from=builder /build/proxy .

# 暴露端口
EXPOSE 8080

# 运行
CMD ["./proxy"]
