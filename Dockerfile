FROM golang:latest AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o server .


FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/server .
RUN ls -l server

EXPOSE 5566

CMD ["./server"]