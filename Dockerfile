# syntax=docker/dockerfile:1

# pagedog: the official image, spoolersh/pagedog, built and published by
# the Makefile's pagedog-image targets. One program: the server runs its
# workers as processes of this image, or as containers of it.
#
# The build stage runs on the host's platform and cross-compiles for the
# target's, so one buildx invocation covers every platform published.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/pagedog ./cmd/pagedog

FROM gcr.io/distroless/static-debian12
ARG VERSION
ARG REVISION
ARG CREATED
LABEL org.opencontainers.image.title="pagedog" \
      org.opencontainers.image.description="Your on-call investigation companion: fetches evidence, sniffs out incidents." \
      org.opencontainers.image.url="https://github.com/spoolersh/pagedog" \
      org.opencontainers.image.source="https://github.com/spoolersh/pagedog" \
      org.opencontainers.image.vendor="Sergey Kamardin" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"
COPY --from=build /out/pagedog /usr/local/bin/
ENTRYPOINT ["pagedog"]
