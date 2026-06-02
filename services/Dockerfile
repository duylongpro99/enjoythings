FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/api ./cmd/api

FROM alpine:3.22

RUN addgroup -S app && adduser -S app -G app
USER app

COPY --from=build /out/api /usr/local/bin/api

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/api"]
