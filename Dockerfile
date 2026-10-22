FROM aquib4040/azml:go

ENV GODEBUG="madvdontneed=1"

WORKDIR /usr/src/app

COPY . .

RUN if [ -f "./azml-go-linux" ]; then cp ./azml-go-linux /usr/local/bin/azml-go && chmod a+rx /usr/local/bin/azml-go; fi

EXPOSE 23434

CMD ["azml-go"]
