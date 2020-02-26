FROM golang:1.14.0-alpine

COPY . /go/src/gatobackup
RUN cd /go/src/gatobackup/ \
  && apk update \
  && apk add build-base gcc \
  && CGO_ENABLED=0 GOOS=linux go install -a -tags netgo -ldflags '-w' . \
  && go test .

CMD ["ls", "-lR", "/go/"]
