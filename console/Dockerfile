FROM node:20-alpine AS build
WORKDIR /app
COPY console/package*.json .
RUN npm install
COPY console/ .
ARG VITE_AUTH_SERVICE_URL
ARG VITE_MANAGEMENT_API_URL
ARG VITE_GATEWAY_URL
ARG VITE_JAEGER_URL
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY console/nginx.conf /etc/nginx/conf.d/default.conf
# Baked in at build time rather than bind-mounted at runtime — see the same
# note on mock-multi-cdn-traffic-manager/Dockerfile: mounting certs/ live
# from a cloud-synced host path (e.g. OneDrive) can hit "resource deadlock
# avoided" (EDEADLK) reading the files.
COPY certs/jpm.com.crt certs/jpm.com.key /certs/
EXPOSE 80 8443
