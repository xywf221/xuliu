# 序流 多阶段构建。
#
# 阶段一编译前端，阶段二编译后端并把前端产物嵌进去，阶段三只留二进制。
# 最终镜像里既没有 Node 也没有 Go 工具链，也没有源码 ——
# 这是一个「自带前端的单文件服务」，运行时只需要一个可执行文件加一份配置。

# ---------------------------------------------------------------------------
# 前端：Vue3 + Vite
# ---------------------------------------------------------------------------
FROM node:24-alpine AS frontend

WORKDIR /build/web

# 先只拷清单再装依赖：只要锁文件没变，这一层就能命中缓存，
# 不至于改一行 Vue 代码就重装一遍 node_modules。
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
# 产物直接落到 ../internal/web/dist，供下一阶段 go:embed 使用。
RUN npm run build


# ---------------------------------------------------------------------------
# 后端：Go
# ---------------------------------------------------------------------------
FROM golang:1.27-alpine AS backend

WORKDIR /build

# go-sqlite3 走纯 Go 实现，不需要 CGO —— 因此可以静态编译，
# 最终镜像也不必带 libc 兼容层。
ENV CGO_ENABLED=0 GOOS=linux

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 用前一阶段真实构建出来的前端产物，覆盖仓库里那份 .gitkeep 占位。
COPY --from=frontend /build/internal/web/dist ./internal/web/dist

# -trimpath 去掉编译机的绝对路径；-s -w 去掉符号表与调试信息。
# 版本号注入进来，`xuliu -version` 与启动日志都能看到。
# 变量必须是 cmd/xuliu 包里的 main.version —— 路径写错时 go build 不报错，
# 只是静默注入失败，版本号一直是 "dev"，等到排查线上问题时才发现。
ARG VERSION=dev
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/xuliu ./cmd/xuliu


# ---------------------------------------------------------------------------
# 运行时：只留二进制、配置与数据目录
# ---------------------------------------------------------------------------
FROM alpine:3.21

# ca-certificates：将来接 S3 之类的 HTTPS 服务要用；
# tzdata：日志与计划日期按本地时区显示才符合直觉。
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 xuliu

WORKDIR /app

COPY --from=backend /out/xuliu /app/xuliu
COPY configs/config.yaml /app/configs/config.yaml

# 数据目录归运行用户所有：sqlite 库与上传的附件都写在这里，
# 挂载卷时也不会因为属主不对而写不进去。
RUN mkdir -p /app/data/uploads && chown -R xuliu:xuliu /app/data

USER xuliu

ENV XULIU_SERVER__ADDR=":8080" \
    XULIU_DATABASE__DSN="/app/data/xuliu.db" \
    XULIU_STORAGE__LOCAL__DIR="/app/data/uploads"

# 用 exec 形式，让进程直接成为 PID 1 的子孙，
# 这样 docker stop 发的 SIGTERM 能直达进程，优雅退出才会生效。
ENTRYPOINT ["/app/xuliu"]
CMD ["-config", "/app/configs/config.yaml"]

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1