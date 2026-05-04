# ── Stage 1: SpotiFLAC 빌드 ──────────────────────────────────────
FROM golang:1.21-alpine AS spotiflac-builder

RUN apk add --no-cache git gcc musl-dev nodejs npm

WORKDIR /spotiflac
RUN git clone https://github.com/Nizarberyan/SpotiFLAC.git .

# headless 모드로 빌드 (GUI 없음)
RUN go build -tags headless -o spotiflac .

# ── Stage 2: HTTP 서버 빌드 ───────────────────────────────────────
FROM golang:1.21-alpine AS server-builder

WORKDIR /server
COPY go.mod ./
COPY main.go ./
RUN go build -o server .

# ── Stage 3: 최종 이미지 ──────────────────────────────────────────
FROM alpine:3.19

# ffmpeg: SpotiFLAC이 오디오 변환에 사용
RUN apk add --no-cache ffmpeg ca-certificates

WORKDIR /app
COPY --from=spotiflac-builder /spotiflac/spotiflac ./spotiflac
COPY --from=server-builder    /server/server       ./server

RUN chmod +x /app/spotiflac /app/server

EXPOSE 8080
CMD ["./server"]
