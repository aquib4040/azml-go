FROM aquib4040/azml:go

ENV GODEBUG="madvdontneed=1"

WORKDIR /usr/src/app

COPY . .

RUN go build -ldflags="-w -s" -o /usr/local/bin/azml-go . && chmod a+rx /usr/local/bin/azml-go

EXPOSE 23434

CMD ["azml-go"]
