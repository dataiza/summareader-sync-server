# Build.
#
# CGO off, because PocketBase uses a pure-Go SQLite. That is what makes the
# runtime image below able to be `scratch`-adjacent: no libc to match, no
# shared objects to carry, and nothing in the image that is not this server.
FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, so a change to the source does not re-download them.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static, stripped, and reproducible enough to compare two builds of the same
# commit. -trimpath keeps the build machine's paths out of the binary.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /summareader-sync .

# Run.
#
# Alpine rather than distroless or scratch, for one reason: pairing the first
# device is a shell command, and a server nobody can get a shell into is a
# server whose first device can never be paired. `docker compose exec` needs
# something to exec.
FROM alpine:3.21

# curl for the healthcheck below, and CA certificates so the server can reach
# anything over TLS later without a mystery failure.
RUN apk add --no-cache ca-certificates curl \
    && adduser -D -u 10001 -h /data allreader

COPY --from=build /summareader-sync /usr/local/bin/summareader-sync

# The database lives here, and this is the only thing worth backing up. It is
# a volume in compose; declared here so `docker run` without one still keeps
# data across a restart rather than silently losing it.
VOLUME /data
WORKDIR /data
USER allreader

EXPOSE 8099

# 0.0.0.0 rather than 127.0.0.1: inside a container, localhost means the
# container, and a server bound there is unreachable from anywhere including
# the host. Exposure is decided by the port mapping, not by this.
CMD ["summareader-sync", "serve", "--http=0.0.0.0:8099", "--dir=/data"]
