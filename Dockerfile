# 多阶段构建：Go 编译 → 精简运行镜像
FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/server /app/server
COPY web/ /app/web/
COPY config.json /app/config.json
COPY config.docker.json /app/config.docker.json
EXPOSE 8642
CMD ["/app/server", "-config", "/app/config.docker.json"]
