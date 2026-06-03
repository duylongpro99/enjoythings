FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG SERVICE=api
RUN go build -o /out/service ./cmd/${SERVICE}

FROM alpine:3.22

RUN addgroup -S app && adduser -S app -G app
USER app

COPY --from=build /out/service /usr/local/bin/service
COPY --from=build /src/db/migrations /src/db/migrations

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/service"]
