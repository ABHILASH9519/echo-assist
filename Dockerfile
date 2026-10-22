FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/echo-assist .

FROM alpine:3.22
RUN adduser -D -H -u 10001 appuser
WORKDIR /app
COPY --from=build /out/echo-assist /app/echo-assist
COPY public /app/public
RUN mkdir -p /app/.data && chown -R appuser:appuser /app
USER appuser
ENV PORT=3000
EXPOSE 3000
ENTRYPOINT ["/app/echo-assist"]
