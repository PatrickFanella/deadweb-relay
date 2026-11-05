FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/relay ./cmd/relay

FROM alpine:3.23@sha256:ff7f0c727dd796aa36787bfcd9a64bf35a6ce72a73451b3359fde849b6703d63
RUN adduser -D -H -u 1000 relay
WORKDIR /app
COPY --from=build /out/relay /usr/local/bin/relay
RUN mkdir -p /app/data/files && chown -R relay:relay /app
USER relay
ENV ADDR=:8080 DATA_DIR=/app/data
EXPOSE 8080
VOLUME ["/app/data"]
CMD ["relay"]
