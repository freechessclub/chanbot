FROM golang:alpine

WORKDIR /go/src/github.com/freechessclub/chanbot
COPY . .

RUN go-wrapper download
RUN go-wrapper install

CMD ["go-wrapper", "run"]
