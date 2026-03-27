# ── Dev stage: Vite dev server with HMR ──
FROM node:22-alpine AS dev

WORKDIR /app

COPY web-dashboard/package.json web-dashboard/package-lock.json ./
COPY packages/ /packages/
RUN npm ci

COPY web-dashboard/ .

EXPOSE 3000
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0", "--port", "3000"]

# ── Build stage ──
FROM node:22-alpine AS build

WORKDIR /app

COPY web-dashboard/package.json web-dashboard/package-lock.json ./
COPY packages/ /packages/
RUN npm ci

COPY web-dashboard/ .
RUN npm run build

# ── Prod stage ──
FROM nginx:alpine AS prod

RUN rm /etc/nginx/conf.d/default.conf
COPY web-dashboard/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 3000

CMD ["nginx", "-g", "daemon off;"]
