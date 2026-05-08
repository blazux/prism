FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o prism .

FROM alpine:3.19
RUN apk add --no-cache docker-cli ca-certificates poppler-utils

WORKDIR /app
COPY --from=builder /build/prism .

EXPOSE 8080

ENV PORT=8080
ENV WORKSPACE_DIR=/workspace
ENV OLLAMA_URL=http://ollama:11434
ENV OLLAMA_MODEL=qwen2.5-coder:7b
ENV AGENT_CONTAINER=prism-workspace

CMD ["./prism"]
