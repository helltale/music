FROM node:22.14.0-bookworm-slim AS build

WORKDIR /src
ENV NEXT_TELEMETRY_DISABLED=1
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM node:22.14.0-bookworm-slim

WORKDIR /app
ENV NODE_ENV=production \
    PORT=3000 \
    HOSTNAME=0.0.0.0
RUN groupadd --gid 10001 app \
    && useradd --uid 10001 --gid 10001 --home-dir /home/app --create-home --shell /usr/sbin/nologin app
COPY --from=build --chown=app:app /src/.next/standalone ./
COPY --from=build --chown=app:app /src/.next/static ./.next/static
COPY --from=build --chown=app:app /src/public ./public
USER app
EXPOSE 3000
CMD ["node", "server.js"]
