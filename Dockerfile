FROM golang:1.26.4-alpine AS build

ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY config.example.yaml ./config.example.yaml
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/playtime-guard ./cmd/playtime-guard

FROM scratch

WORKDIR /app
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/playtime-guard /usr/local/bin/playtime-guard
COPY config.example.yaml /app/config.example.yaml

USER 1000:1000
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["playtime-guard", "healthcheck", "http://127.0.0.1:8080/healthz"]

ENTRYPOINT ["playtime-guard"]
CMD ["-config", "/app/config.yaml"]
