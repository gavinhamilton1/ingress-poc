FROM node:20-alpine
WORKDIR /app
# The function code will be injected via environment variable FUNCTION_CODE
# or mounted as /app/handler.js
COPY server.js /app/server.js
CMD ["node", "/app/server.js"]
