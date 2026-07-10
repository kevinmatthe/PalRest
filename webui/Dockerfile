FROM node:25-alpine AS build

WORKDIR /src
COPY package.json package-lock.json* .npmrc ./
RUN npm ci

COPY index.html tsconfig.json tsconfig.node.json vite.config.ts ./
COPY public ./public
COPY src ./src
RUN npm run build

FROM caddy:2.9-alpine

WORKDIR /srv
COPY --from=build /src/dist /srv
COPY Caddyfile /etc/caddy/Caddyfile
COPY docker-entrypoint.sh /usr/local/bin/palrest-webui-entrypoint
RUN chmod +x /usr/local/bin/palrest-webui-entrypoint

EXPOSE 8080
ENTRYPOINT ["palrest-webui-entrypoint"]
