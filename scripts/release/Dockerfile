# The container for a released headless bundle.
#
# Separate from the Dockerfile at the root of the repository, and the
# difference is the whole point: that one compiles the server from source,
# which a download has none of. This one copies the binary the release already
# built. No Go, no build stage, and a build that finishes in about a second.
#
# Everything below the first stage is deliberately identical to the repository
# Dockerfile. Two images that differ in how they were made and not in what they
# are is the only version of this worth shipping.
FROM alpine:3.21

# curl for the healthcheck in the compose file, and CA certificates so the
# server can reach anything over TLS later without a mystery failure.
RUN apk add --no-cache ca-certificates curl \
    && adduser -D -u 10001 -h /data summareader

# Built by the release workflow for this image's architecture. A bundle for a
# different architecture carries a different binary under the same name.
COPY summareader-sync /usr/local/bin/summareader-sync

# The database lives here, and this is the only thing worth backing up. It is
# a volume in compose; declared here so `docker run` without one still keeps
# data across a restart rather than silently losing it.
VOLUME /data
WORKDIR /data
USER summareader

EXPOSE 8099

# 0.0.0.0 rather than 127.0.0.1: inside a container, localhost means the
# container, and a server bound there is unreachable from anywhere including
# the host. Exposure is decided by the port mapping, not by this.
CMD ["summareader-sync", "serve", "--http=0.0.0.0:8099", "--dir=/data"]
