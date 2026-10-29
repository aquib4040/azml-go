FROM golang:bookworm AS builder

WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /usr/local/bin/azml-go .

FROM aquib4040/azmlgo:latest

ENV GODEBUG="madvdontneed=1"
ENV TZ="Asia/Kolkata"
ENV DEBIAN_FRONTEND="noninteractive"

WORKDIR /usr/src/app

# Install git, speedtest-cli, curl, ca-certificates, and tzdata without interactive debconf prompts
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends git speedtest-cli curl ca-certificates tzdata && \
    ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone && \
    rm -rf /var/lib/apt/lists/*

# Copy the exact Go compiler from builder stage (no re-downloading required)
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

# Pre-cache Go modules from builder stage for instant builds
COPY --from=builder /go/pkg/mod /go/pkg/mod
ENV GOPATH="/go"

RUN mkdir -p /usr/src/app/downloads /usr/src/app/Thumbnails

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

# Copy source repository to /usr/src/app
COPY . /usr/src/app
RUN chmod -R 777 /usr/src/app

COPY --from=builder /usr/local/bin/azml-go /usr/local/bin/azml-go
RUN chmod a+rx /usr/local/bin/azml-go

EXPOSE 23434

CMD ["azml-go"]
