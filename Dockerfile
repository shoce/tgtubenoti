
# https://hub.docker.com/_/golang/tags
FROM golang:1.26-alpine AS build
ENV CGO_ENABLED=0
RUN mkdir -p /tgtubenoti/
COPY *.go go.mod go.sum /tgtubenoti/
WORKDIR /tgtubenoti/
RUN go version
RUN go get -v
RUN ls -l -a
RUN go build -o tgtubenoti .
RUN ls -l -a


# https://hub.docker.com/_/alpine/tags
FROM alpine:3
RUN apk add --no-cache tzdata gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2
RUN mkdir -p /tgtubenoti/
WORKDIR /tgtubenoti/
COPY --from=build /tgtubenoti/tgtubenoti /tgtubenoti/tgtubenoti
RUN ls -l -a /tgtubenoti/tgtubenoti
ENTRYPOINT ["/tgtubenoti/tgtubenoti"]


