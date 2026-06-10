# Build the manager binary.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -o /manager ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
