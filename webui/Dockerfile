# Build context: playtime-guard repository root (see sidecars.yaml).
FROM alpine:3.21 AS map-tiles
RUN apk add --no-cache git git-lfs
WORKDIR /work
COPY .gitattributes ./
COPY webui/public/map ./webui/public/map
# Copy .git when available so git lfs pull can materialise pointer files.
# If COPY fails in some environments, the later pointer check documents the failure mode.
COPY .git ./.git
RUN git lfs install \
 && git lfs pull --include="webui/public/map/tiles/**" --exclude="" \
 || echo "map-tiles: git lfs pull skipped or failed; using copied working tree"

FROM node:25-alpine AS build
WORKDIR /src
COPY webui/package.json webui/package-lock.json* webui/.npmrc ./
RUN npm ci
COPY webui/index.html webui/tsconfig.json webui/tsconfig.node.json webui/vite.config.ts ./
COPY webui/src ./src
COPY webui/public ./public
COPY --from=map-tiles /work/webui/public/map ./public/map
RUN if [ -f public/map/tiles/0/0/0.png ] && head -c 50 public/map/tiles/0/0/0.png | grep -q 'git-lfs'; then \
      echo "ERROR: map tiles are still Git LFS pointers after pull stage" >&2; exit 1; \
    fi
RUN npm run build

FROM caddy:2.9-alpine
WORKDIR /srv
COPY --from=build /src/dist /srv
COPY webui/Caddyfile /etc/caddy/Caddyfile
COPY webui/docker-entrypoint.sh /usr/local/bin/palrest-webui-entrypoint
RUN chmod +x /usr/local/bin/palrest-webui-entrypoint
EXPOSE 8080
ENTRYPOINT ["palrest-webui-entrypoint"]
