# Build from the repository root:
#   docker build -f examples/docker/Dockerfile -t stats-docker-example .
# The Go version matches the `go` line of go.mod.
FROM golang:1.26.0-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/demo ./examples/docker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/demo /demo
ENTRYPOINT ["/demo"]
