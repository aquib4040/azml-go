FROM aquib4040/azml:go

ENV GODEBUG="madvdontneed=1"

WORKDIR /usr/src/app

COPY . .


EXPOSE 23434

CMD ["azml-go"]
