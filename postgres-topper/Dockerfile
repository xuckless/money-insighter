# Build stage: static binary, no cgo, symbols stripped.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/topper ./cmd/topper

# Runtime: distroless, non-root, no shell. There is nothing to exec into.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/topper /topper
EXPOSE 8080
ENTRYPOINT ["/topper"]
