FROM golang:1.16 as build-env

WORKDIR /go/src/app
ADD . /go/src/app

RUN go get -d -v ./...

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -tags netgo -ldflags '-w' -o /go/bin/app

FROM gcr.io/distroless/static
COPY --from=build-env /go/bin/app /

CMD ["/app"]
EXPOSE 8888
