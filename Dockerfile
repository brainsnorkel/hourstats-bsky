FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /hourstats ./cmd/hourstats
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /import-dynamodb ./cmd/import-dynamodb
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /force-trending ./cmd/force-trending
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /cleanup-kv ./cmd/cleanup-kv

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata sqlite

COPY --from=builder /hourstats /usr/local/bin/hourstats
COPY --from=builder /import-dynamodb /usr/local/bin/import-dynamodb
COPY --from=builder /force-trending /usr/local/bin/force-trending
COPY --from=builder /cleanup-kv /usr/local/bin/cleanup-kv

ENTRYPOINT ["/usr/local/bin/hourstats"]
