
# https://hub.docker.com/_/golang/tags
FROM golang:1.22.0 as build
RUN mkdir -p /root/tgtubenotibot/
COPY tgtubenotibot.go go.mod go.sum /root/tgtubenotibot/
WORKDIR /root/tgtubenotibot/
RUN go version
RUN go get -a -v
RUN ls -l -a
RUN go build -o tgtubenotibot tgtubenotibot.go
RUN ls -l -a


# https://hub.docker.com/_/alpine/tags
FROM alpine:3.18.6
RUN apk add --no-cache tzdata
RUN apk add --no-cache gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2
RUN mkdir -p /opt/tgtubenotibot/
COPY --from=build /root/tgtubenotibot/tgtubenotibot /opt/tgtubenotibot/tgtubenotibot
RUN ls -l -a /opt/tgtubenotibot/
WORKDIR /opt/tgtubenotibot/
ENTRYPOINT ["./tgtubenotibot"]


