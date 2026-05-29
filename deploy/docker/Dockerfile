FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/shark-socket-new ./cmd/shark-socket-new

FROM alpine:3.22
RUN adduser -D -H shark
USER shark
COPY --from=build /out/shark-socket-new /usr/local/bin/shark-socket-new
EXPOSE 18000 18080 18081
ENTRYPOINT ["/usr/local/bin/shark-socket-new"]
