# syntax=docker/dockerfile:1.7

# Local build image. CI / release builds use Dockerfile.release with the
# binary already produced by goreleaser.
FROM golang:1.26-alpine AS build
WORKDIR /src

RUN apk add --no-cache git ca-certificates && update-ca-certificates

COPY go.mod ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/annoying-aup-filter ./cmd/annoying-aup-filter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/annoying-aup-filter /usr/local/bin/annoying-aup-filter

# Bind the proxy to all interfaces inside the container; the UI stays
# behind the explicit -ui-addr 0.0.0.0:8888 so it's reachable when the
# port is published.
EXPOSE 8080 8888
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/annoying-aup-filter"]
CMD ["-proxy-addr", "0.0.0.0:8080", "-ui-addr", "0.0.0.0:8888"]
