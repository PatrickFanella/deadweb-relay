FROM golang:1.23-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/relay ./cmd/relay

FROM alpine:3.20
RUN adduser -D -H relay
WORKDIR /app
COPY --from=build /out/relay /usr/local/bin/relay
RUN mkdir -p /app/data/files && chown -R relay:relay /app
USER relay
ENV ADDR=:8080 DATA_DIR=/app/data
EXPOSE 8080
VOLUME ["/app/data"]
CMD ["relay"]
