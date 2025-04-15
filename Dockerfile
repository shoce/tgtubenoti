
# https://hub.docker.com/_/golang/tags
FROM golang:1.24.2 AS build
ENV CGO_ENABLED=0
RUN mkdir -p /root/tgtubenoti/
COPY *.go go.mod go.sum /root/tgtubenoti/
WORKDIR /root/tgtubenoti/
RUN go version
RUN go get -v
RUN ls -l -a
RUN go build -o tgtubenoti .
RUN ls -l -a


# https://hub.docker.com/_/alpine/tags
FROM alpine:3.21.3
RUN apk add --no-cache tzdata
RUN apk add --no-cache gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2
COPY --from=build /root/tgtubenoti/tgtubenoti /bin/tgtubenoti
RUN ls -l -a /bin/tgtubenoti
RUN mkdir -p /opt/tgtubenoti/
WORKDIR /opt/tgtubenoti/
ENTRYPOINT ["/bin/tgtubenoti"]


