FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN go build -trimpath -ldflags='-s -w' -o /out/gateway ./cmd/gateway

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/gateway /usr/local/bin/gateway
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/gateway"]
