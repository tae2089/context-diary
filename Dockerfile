# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/context-diary ./cmd/context-diary

FROM alpine:3.21
# git: explain/explain_function shell out to `git log -L` (mirrors are bare
# repos, no worktree needed). ca-certificates: GitHub API. tzdata: local
# time rendering in explain output and check pages.
RUN apk add --no-cache git ca-certificates tzdata \
    && adduser -D -H -u 65532 ctxdiary \
    && mkdir -p /var/cache/context-diary \
    && chown ctxdiary:ctxdiary /var/cache/context-diary
COPY --from=build /out/context-diary /usr/local/bin/context-diary
USER ctxdiary
# os.UserCacheDir honors XDG_CACHE_HOME; serve keeps bare mirrors under it.
# Mount a volume here to persist mirrors across container restarts
# (optional — a lost cache just re-clones on the next merge).
ENV XDG_CACHE_HOME=/var/cache/context-diary
EXPOSE 8080
# Meaningful for the default `serve` command; harmless for one-shot
# subcommands (docker only reports health on long-running containers).
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["context-diary"]
CMD ["serve"]
