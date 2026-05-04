# ── Stage 1: SpotiFLAC 빌드 ──────────────────────────────────────
FROM golang:1.24-alpine AS spotiflac-builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /spotiflac
RUN git clone https://github.com/Nizarberyan/SpotiFLAC.git .

# go.mod 버전 패치 후 tidy → build
RUN sed -i 's/^go 1\.25.*/go 1.24/' go.mod && \
    go mod tidy && \
    go build -tags headless -o spotiflac .

# ── Stage 2: HTTP 서버 빌드 ───────────────────────────────────────
FROM golang:1.24-alpine AS server-builder

WORKDIR /server
COPY go.mod ./
COPY main.go ./
RUN go build -o server .

# ── Stage 3: 최종 이미지 ──────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ffmpeg ca-certificates

WORKDIR /app
COPY --from=spotiflac-builder /spotiflac/spotiflac ./spotiflac
COPY --from=server-builder    /server/server       ./server

RUN chmod +x /app/spotiflac /app/server

EXPOSE 8080
CMD ["./server"]
