# Build stage: static binary, no cgo, symbols stripped.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/plaidsync ./cmd/plaidsync

# Runtime: distroless, non-root, no shell. Migrations are embedded in the
# binary and applied at startup.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/plaidsync /plaidsync
EXPOSE 8080
ENTRYPOINT ["/plaidsync"]
