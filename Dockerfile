FROM golang:1.21 AS builder

WORKDIR /app

COPY go.mod go.sum /app
RUN go mod download && go mod verify

COPY main.go /app
RUN CGO_ENABLED=0 go build -o /app/main ./...

FROM scratch
COPY --from=builder /app/main /usr/local/bin/main

CMD [ "/usr/local/bin/main" ]
