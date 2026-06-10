# Multi-stage build for the instrumented echo service.
# Uses the committed go.mod + go.sum for a reproducible, pinned build.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /echo-svc .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /echo-svc /echo-svc
EXPOSE 8080
ENTRYPOINT ["/echo-svc"]
