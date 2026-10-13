# Member web app image (spec #24): Nuxt 3 SSR, runtime hosts only
# (NUXT_PUBLIC_API_BASE_URL / NUXT_PUBLIC_WS_URL from the environment,
# never baked). Prune devDependencies via npm ci --omit=dev; the
# standalone server runs as the node user.
FROM docker.io/library/node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM docker.io/library/node:22-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /app/.output ./.output
EXPOSE 3000
USER node
CMD ["node", ".output/server/index.mjs"]
