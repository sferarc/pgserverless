# Build from the root of a github.com/sferarc/pgserverless checkout:
#   docker build -f cmd/pgserverless/Dockerfile -t pgserverless .
# The build stage runs on the builder's own platform and cross-compiles, so a multi-arch build compiles no Go under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

ARG TARGETOS
ARG TARGETARCH

# A static binary lets the final stage carry no libc.
ENV CGO_ENABLED=0

WORKDIR /src

# Dependencies first, so a source-only change does not download the module graph again.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/pgserverless ./cmd/pgserverless

FROM alpine:3.24

# Without the CA bundle, TLS to a managed database fails with an x509 error.
RUN apk add --no-cache ca-certificates

RUN adduser -D -u 10001 pgserverless
USER pgserverless

COPY --from=build /out/pgserverless /usr/local/bin/pgserverless

EXPOSE 4000

# Inside the container :4000 is not loopback, so the server still needs PGSERVERLESS_AUTH_TOKEN or PGSERVERLESS_ALLOW_REMOTE=true to start.
ENV PGSERVERLESS_ADDR=:4000

ENTRYPOINT ["/usr/local/bin/pgserverless"]
