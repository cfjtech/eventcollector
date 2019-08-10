FROM golang:1.11-alpine3.8

RUN apk update && apk add git

COPY . $GOPATH/src/eventcollector/
WORKDIR $GOPATH/src/eventcollector/

#get dependancies
#you can also use dep
RUN go get -d -v
#build the binary
RUN go build -o /go/bin/eventcollector

EXPOSE 8080

ENTRYPOINT ["/go/bin/eventcollector"]
