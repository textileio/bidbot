# syntax = docker/dockerfile:1-experimental

FROM golang:1.16-buster as builder

RUN mkdir /app 
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download -x
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
  BIN_BUILD_FLAGS="CGO_ENABLED=0 GOOS=linux" make build-bidbot

FROM alpine
COPY --from=builder /app/bidbot /app/bidbot
COPY --from=builder /app/cmd/bidbot/bin/container_daemon /app/start_bidbot
WORKDIR /app

ENV BIDBOT_PATH /data/bidbot
RUN mkdir -p $BIDBOT_PATH \
  && adduser -D -h $BIDBOT_PATH -u 1000 -G users bidbot \
  && chown bidbot:users $BIDBOT_PATH
USER bidbot
VOLUME $BIDBOT_PATH

ENTRYPOINT ["./start_bidbot"]
CMD ["daemon"]
