# 构建阶段
FROM node:22-alpine AS webbuild
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm install
COPY web/ ./
RUN npm run build

# 运行阶段
FROM golang:1.25-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN go vet ./...
RUN go test ./internal/agent/ -count=1
RUN CGO_ENABLED=1 go build -o /atomix .

FROM alpine:3.20
RUN apk add --no-cache sqlite-libs
WORKDIR /app
COPY --from=build /atomix /app/atomix
COPY --from=webbuild /web/dist /app/static
ENV ATOMIX_PORT=8080 ATOMIX_DATA_DIR=/app/data
EXPOSE 8080
CMD ["/app/atomix"]
