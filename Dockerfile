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
# ATOMIX_DEPLOY_SHA：构建时注入的部署版本标识（git short SHA），health 接口返回供核对
ARG ATOMIX_DEPLOY_SHA=unset
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN go vet ./...
RUN go test ./internal/agent/ -count=1
RUN CGO_ENABLED=1 go build -ldflags "-X atomix-demo/server/internal/config.BuiltinDeploySHA=${ATOMIX_DEPLOY_SHA}" -o /atomix .

FROM alpine:3.20
RUN apk add --no-cache sqlite-libs
WORKDIR /app
COPY --from=build /atomix /app/atomix
COPY --from=webbuild /web/dist /app/static
ENV ATOMIX_PORT=8080 ATOMIX_DATA_DIR=/app/data
# 运行时再兜底一份：未传 build-arg 时 health 也能区分镜像来源
ARG ATOMIX_DEPLOY_SHA=unset
ENV ATOMIX_DEPLOY_SHA=${ATOMIX_DEPLOY_SHA}
EXPOSE 8080
CMD ["/app/atomix"]
