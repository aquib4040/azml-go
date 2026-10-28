FROM golang:alpine AS builder

WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /usr/local/bin/azml-go .

FROM aquib4040/azmlgo:latest

ENV GODEBUG="madvdontneed=1"

WORKDIR /usr/src/app

RUN apt-get update && apt-get install -y --no-install-recommends speedtest-cli curl && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /usr/src/app/downloads /usr/src/app/Thumbnails && chmod -R 777 /usr/src/app

# Install HatchZip for in-place, space-saving archive extraction
RUN (curl -fsSL https://raw.githubusercontent.com/xevrion/hatchzip/main/install.sh | HATCHZIP_INSTALL_DIR=/usr/local/bin sh) \
    || ( \
        ARCH=$(uname -m) && \
        case "$ARCH" in \
            x86_64|amd64) HZ_ARCH="x86_64" ;; \
            aarch64|arm64) HZ_ARCH="aarch64" ;; \
            *) HZ_ARCH="x86_64" ;; \
        esac && \
        curl -fsSL "https://github.com/xevrion/hatchzip/releases/latest/download/hatchzip-${HZ_ARCH}-linux.tar.gz" -o /tmp/hatchzip.tar.gz && \
        tar -xzf /tmp/hatchzip.tar.gz -C /tmp && \
        cp "/tmp/hatchzip-${HZ_ARCH}-linux/hatchzip" /usr/local/bin/hatchzip && \
        chmod +x /usr/local/bin/hatchzip && \
        ln -sf /usr/local/bin/hatchzip /usr/local/bin/hz && \
        rm -rf /tmp/hatchzip* \
    ) && (hatchzip --version || true)

COPY --from=builder /usr/local/bin/azml-go /usr/local/bin/azml-go
RUN chmod a+rx /usr/local/bin/azml-go

EXPOSE 23434

CMD ["azml-go"]
