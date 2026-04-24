FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN go mod tidy
RUN go build -o /bin/main-service .

FROM alpine:3.20
WORKDIR /app
COPY --from=build /bin/main-service /app/main-service
EXPOSE 8080
CMD ["/app/main-service"]
