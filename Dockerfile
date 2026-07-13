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

FROM python:3.11-slim AS save-worker

ARG PALWORLD_SAVE_TOOLS_REF=673505c1abdb143fb3835213b0a1ed57bd3cdd0b
ARG PIP_INDEX_URL=https://pypi.org/simple
ARG PIP_EXTRA_INDEX_URL=

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates g++ git \
    && rm -rf /var/lib/apt/lists/*
RUN git clone https://github.com/deafdudecomputers/PalworldSaveTools.git /tmp/PalworldSaveTools \
    && cd /tmp/PalworldSaveTools \
    && git checkout ${PALWORLD_SAVE_TOOLS_REF}
RUN python -m venv /opt/palrest-save-worker \
    && /opt/palrest-save-worker/bin/pip install --no-cache-dir --upgrade pip setuptools wheel \
    && /opt/palrest-save-worker/bin/pip install --no-cache-dir \
        /tmp/PalworldSaveTools/src/palsav/palooz \
        /tmp/PalworldSaveTools/src/palsav \
        orjson \
    && rm -rf /tmp/PalworldSaveTools

FROM python:3.11-slim

WORKDIR /app
COPY --from=build /out/playtime-guard /usr/local/bin/playtime-guard
COPY --from=save-worker /opt/palrest-save-worker /opt/palrest-save-worker
COPY config.example.yaml /app/config.example.yaml
COPY tools/save_worker/palrest_save_worker.py /usr/local/bin/palrest-save-worker
RUN chmod 0755 /usr/local/bin/palrest-save-worker

USER 1000:1000
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["playtime-guard", "healthcheck", "http://127.0.0.1:8080/healthz"]

ENTRYPOINT ["playtime-guard"]
CMD ["-config", "/app/config.yaml"]
