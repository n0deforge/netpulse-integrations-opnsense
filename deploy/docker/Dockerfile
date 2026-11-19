# Pushers image: opnsense-pusher and wifictl-pusher, static, non-root.
# Build context is the repository root:
#   docker build -f deploy/docker/Dockerfile -t netpulse-pushers .
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" \
      -o /out/opnsense-pusher ./opnsense/cmd/opnsense-pusher \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" \
      -o /out/wifictl-pusher ./wifictl/cmd/wifictl-pusher

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 1000 pusher
COPY --from=build /out/ /usr/local/bin/
USER pusher
