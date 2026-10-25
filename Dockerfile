FROM golang:1.24-alpine AS builder

WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /usr/local/bin/azml-go .

FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive \
    GODEBUG="madvdontneed=1"

WORKDIR /usr/src/app

RUN apt-get update && apt-get install -y --no-install-recommends \
    aria2 \
    ffmpeg \
    mediainfo \
    p7zip-full \
    speedtest-cli \
    curl \
    git \
    ca-certificates \
    python3 \
    python3-pip \
    && curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp \
    && chmod a+rx /usr/local/bin/yt-dlp \
    && rm -rf /var/lib/apt/lists/*

COPY . .

RUN mkdir -p /usr/src/app/downloads /usr/src/app/Thumbnails && chmod -R 777 /usr/src/app

COPY --from=builder /usr/local/bin/azml-go /usr/local/bin/azml-go
RUN chmod a+rx /usr/local/bin/azml-go

EXPOSE 23434

CMD ["azml-go"]
