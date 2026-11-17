# syntax=docker/dockerfile:1

# demoapp is the service the demo watches over: its own module, built from
# this directory alone, so the demo needs nothing from the repository root.
FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/demoapp .

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/demoapp /usr/local/bin/
ENTRYPOINT ["demoapp"]
