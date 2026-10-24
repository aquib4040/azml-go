FROM aquib4040/azml:go

ENV PATH="/usr/local/go/bin:${PATH}"
ENV GODEBUG="madvdontneed=1"

WORKDIR /usr/src/app

RUN (python3 -m pip install --no-cache-dir speedtest-cli || pip3 install --no-cache-dir speedtest-cli || apt-get update && apt-get install -y speedtest-cli || apk add --no-cache speedtest-cli) || true

COPY . .

RUN mkdir -p /usr/src/app/downloads /usr/src/app/Thumbnails && chmod -R 777 /usr/src/app

RUN go build -ldflags="-w -s" -o /usr/local/bin/azml-go . && chmod a+rx /usr/local/bin/azml-go

EXPOSE 23434

CMD ["azml-go"]
