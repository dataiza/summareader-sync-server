#!/bin/sh
# Build the image and push it to the cluster's registry, tagged with the chart version
# (Chart.yaml is the one place the version is bumped; a test keeps it equal to version.go).
set -eu
cd "$(dirname "$0")/.."
registry=${REGISTRY:-10.34.1.1:30095}
v=$(sed -n 's/^version: *//p' helm/summareader-sync/Chart.yaml | tr -d '"')
[ -n "$v" ] || { echo "no version: in helm/summareader-sync/Chart.yaml" >&2; exit 1; }
docker build -t "$registry/summareader-sync:$v" . && docker push "$registry/summareader-sync:$v"
