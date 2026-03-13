FROM node:22-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/build web/build
RUN CGO_ENABLED=1 go build -tags "sqlite_fts5" -o nexus ./cmd/nexus/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates nodejs npm python3 uv
WORKDIR /app
COPY --from=build /app/nexus .
VOLUME /data
ENV DATA_DIR=/data
EXPOSE 8080 2525
CMD ["./nexus", "serve"]
