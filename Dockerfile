FROM golang:1.25.5-alpine AS builder

WORKDIR /build
COPY . .
RUN go build -ldflags="-s -w" -o gilebrowser .

FROM alpine:latest

RUN adduser -D -H gilebrowser
USER gilebrowser

COPY --from=builder /build/gilebrowser /usr/local/bin/gilebrowser

EXPOSE 7887

ENTRYPOINT ["gilebrowser"]
