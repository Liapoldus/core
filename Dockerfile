FROM golang:1.26 AS build
WORKDIR /workspace/core
COPY core/go.mod core/go.sum ./
COPY plugin-sdk /workspace/plugin-sdk
COPY core .
RUN go build -trimpath -ldflags='-s -w' -o /out/core ./cmd/core

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/core /usr/local/bin/core
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/core"]
