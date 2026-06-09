# ---- 构建阶段 ----
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 依赖缓存
COPY go.mod go.sum ./
RUN go mod download

# 编译
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o feishu-passwd-bot .

# ---- 运行阶段 ----
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

WORKDIR /app
COPY --from=builder /app/feishu-passwd-bot .

EXPOSE 8080

ENTRYPOINT ["./feishu-passwd-bot"]
