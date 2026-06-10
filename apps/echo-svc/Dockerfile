# Multi-stage build for the instrumented echo service.
# `go mod tidy` resolves deps + writes go.sum at build time (needs network once).
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN go mod tidy && CGO_ENABLED=0 go build -trimpath -o /echo-svc .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /echo-svc /echo-svc
EXPOSE 8080
ENTRYPOINT ["/echo-svc"]
