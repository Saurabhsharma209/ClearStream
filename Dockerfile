FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o clearstream ./cmd/clearstream

FROM alpine:3.19
RUN apk add --no-cache ffmpeg ca-certificates
COPY --from=builder /app/clearstream /usr/local/bin/clearstream
EXPOSE 8080
EXPOSE 5004/udp
ENTRYPOINT ["clearstream", "server", "--http", ":8080"]
