FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Migrations são embutidas no binário (package migrations); cmd/migrate vai junto para
# baseline/force manual dentro do container.
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -o migrate ./cmd/migrate

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server /app/migrate ./
EXPOSE 8080
CMD ["./server"]
