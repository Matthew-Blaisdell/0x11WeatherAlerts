FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /weatheralerts ./cmd/weatheralerts
RUN mkdir /data

# distroless/static: CA certs, no shell. SQLite is pure Go, so no libc needed.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /weatheralerts /weatheralerts
# Pre-create /data owned by nonroot so a fresh named volume is writable.
COPY --from=build --chown=65532:65532 /data /data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/weatheralerts", "-config", "/config/config.yaml"]
