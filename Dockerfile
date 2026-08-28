# ---- SPA build stage ----
FROM node:22-alpine AS spa
WORKDIR /spa
COPY web/spa/package.json web/spa/package-lock.json ./
RUN npm ci
COPY web/spa/ ./
RUN npm run build   # -> /spa/dist

# ---- Go build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Use the freshly built SPA so the embedded assets are always current.
RUN rm -rf web/spa/dist
COPY --from=spa /spa/dist web/spa/dist
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
EXPOSE 8080
CMD ["/app/server"]
