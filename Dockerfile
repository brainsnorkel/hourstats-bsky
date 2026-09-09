FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /hourstats ./cmd/hourstats
# One-off admin tool for the sentiment realignment; see docs/SENTIMENT_REALIGNMENT_PLAN.md
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /realign ./cmd/realign

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata sqlite

COPY --from=builder /hourstats /usr/local/bin/hourstats
COPY --from=builder /realign /usr/local/bin/realign

ENTRYPOINT ["/usr/local/bin/hourstats"]
