# ── Stage 1: Go 서버 빌드 ─────────────────────────────────────────
FROM golang:1.24-alpine AS server-builder

WORKDIR /server
COPY go.mod ./
COPY main.go ./
RUN go build -o server .

# ── Stage 2: 최종 이미지 ──────────────────────────────────────────
FROM python:3.12-slim

# ffmpeg + yt-dlp 설치
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir yt-dlp

WORKDIR /app
COPY --from=server-builder /server/server ./server

RUN chmod +x /app/server

EXPOSE 8080
CMD ["./server"]
