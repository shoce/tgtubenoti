
# https://hub.docker.com/_/golang/tags
FROM golang:1.23.4 as build
RUN mkdir -p /root/tgtubenoti/
COPY tgtubenoti.go go.mod go.sum /root/tgtubenoti/
WORKDIR /root/tgtubenoti/
RUN go version
RUN go get -a -v
RUN ls -l -a
RUN go build -o tgtubenoti tgtubenoti.go
RUN ls -l -a


# https://hub.docker.com/_/alpine/tags
FROM alpine:3.20.3
RUN apk add --no-cache tzdata
RUN apk add --no-cache gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2
RUN mkdir -p /opt/tgtubenoti/
COPY --from=build /root/tgtubenoti/tgtubenoti /opt/tgtubenoti/tgtubenoti
RUN ls -l -a /opt/tgtubenoti/
WORKDIR /opt/tgtubenoti/
ENTRYPOINT ["./tgtubenoti"]


