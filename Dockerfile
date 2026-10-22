FROM aquib4040/azml:go

WORKDIR /usr/src/app

COPY . .

EXPOSE 23434

CMD ["azml-go"]
