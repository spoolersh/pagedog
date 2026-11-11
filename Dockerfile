# syntax=docker/dockerfile:1

# One build for the three programs; each gets its own image below.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out && CGO_ENABLED=0 go build -trimpath -o /out/ ./cmd/pagedog ./cmd/worker ./cmd/demoapp

# pagedog is the launcher. It carries the worker too, for the exec runtime;
# the docker runtime uses the worker image instead.
FROM gcr.io/distroless/static-debian12 AS pagedog
COPY --from=build /out/pagedog /out/worker /usr/local/bin/
ENTRYPOINT ["pagedog"]

# worker is what the launcher runs per incident, one container each.
FROM gcr.io/distroless/static-debian12 AS worker
COPY --from=build /out/worker /usr/local/bin/
ENTRYPOINT ["worker"]

# demoapp is the service the demo watches over.
FROM gcr.io/distroless/static-debian12 AS demoapp
COPY --from=build /out/demoapp /usr/local/bin/
ENTRYPOINT ["demoapp"]
