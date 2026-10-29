# ---------- 1. 前端构建 ----------
FROM node:22-alpine AS web
WORKDIR /src/web

# 只有依赖清单变了才重装依赖
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund 2>/dev/null || npm install --no-audit --no-fund

COPY web/ ./
RUN npm run build


# ---------- 2. 后端构建 ----------
# 关键：用 --platform=$BUILDPLATFORM 让这一层跑在「构建机自身架构」上，
# 再靠 Go 的交叉编译产出目标架构二进制。
# 因为 CGO_ENABLED=0，Go 不依赖 C 工具链，可以直接交叉编译；
# 否则 arm64 会在 QEMU 模拟下编译 modernc.org/sqlite（体量极大），耗时以小时计。
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS api
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src

# 内容抓取是构建前的一次性动作，产物已在仓库里（content/），
# 这里只把前端产物搬进来再编译，构建阶段无需联网抓取任何东西。
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /src/web/dist ./web/dist

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags "-s -w -X learnsomething/internal/server.Version=${VERSION}" \
      -o /out/learn ./cmd/server

# 交叉编译产物做一次架构自检，避免推错架构的镜像
RUN test -f /out/learn && ls -lh /out/learn


# ---------- 3. 运行时 ----------
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -H -u 10001 app && \
    mkdir -p /data && chown app:app /data

WORKDIR /app
COPY --from=api /out/learn ./learn

USER app
ENV ADDR=":8080" \
    DATA_DIR="/data" \
    TZ="Asia/Shanghai"

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/learn"]
