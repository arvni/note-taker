# ---- build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src
# cache deps
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# static binaries
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -ldflags="-s -w" -o /out/server ./cmd/server \
 && go build -ldflags="-s -w" -o /out/worker ./cmd/worker

# ---- runtime stage ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
USER app
WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=build /out/worker /app/worker
# The server serves HTTP; TLS is terminated by the Caddy reverse proxy.
EXPOSE 8080
ENTRYPOINT ["/app/server"]
