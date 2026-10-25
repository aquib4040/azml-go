FROM golang:alpine AS builder

WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /usr/local/bin/azml-go .

FROM aquib4040/azml:go

ENV GODEBUG="madvdontneed=1"

WORKDIR /usr/src/app

RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg mediainfo speedtest-cli curl && rm -rf /var/lib/apt/lists/*

COPY . .

RUN mkdir -p /usr/src/app/downloads /usr/src/app/Thumbnails && chmod -R 777 /usr/src/app

COPY --from=builder /usr/local/bin/azml-go /usr/local/bin/azml-go
RUN chmod a+rx /usr/local/bin/azml-go

EXPOSE 23434

CMD ["azml-go"]
