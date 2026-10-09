FROM golang:1.26 AS build
WORKDIR /workspace
COPY plugin-sdk /workspace/plugin-sdk
COPY core /workspace/core
RUN go work init ./core ./plugin-sdk
RUN go work edit -go=1.26.0 go.work
WORKDIR /workspace/core
RUN go build -trimpath -ldflags='-s -w' -o /out/core ./cmd/core

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/core /usr/local/bin/core
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/core"]
