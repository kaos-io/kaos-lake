# syntax=docker/dockerfile:1
#
# kaos-lake: the KAOS distribution of lakeFS v1.86.0 (Apache-2.0). See KAOS-FORK.md.
#
# Self-contained build (web UI included) so one kaniko step in Argo Workflows builds the
# image from the repository root:
#   kaniko --context=dir://<repo> --dockerfile=kaos/Dockerfile --build-arg VERSION=1.86.0-kaos.N
# Same runtime layout as upstream's `lakefs` target (/app/lakefs, user lakefs, port 8000),
# so the lakeFS Helm chart runs it unchanged. No BuildKit-only syntax (kaniko).

ARG VERSION=dev

FROM node:22.17-alpine AS ui
WORKDIR /webui
COPY webui/package.json webui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY webui/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
ARG VERSION
RUN apk add -U --no-cache build-base ca-certificates
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=ui /webui/dist ./webui/dist
RUN go build -ldflags "-X github.com/treeverse/lakefs/pkg/version.Version=${VERSION}" -o lakefs ./cmd/lakefs \
 && go build -ldflags "-X github.com/treeverse/lakefs/pkg/version.Version=${VERSION}" -o lakectl ./cmd/lakectl

FROM alpine:3.24
ARG VERSION
LABEL org.opencontainers.image.title="kaos-lake" \
      org.opencontainers.image.description="KAOS distribution of lakeFS v1.86.0" \
      org.opencontainers.image.source="https://github.com/kaos-io/lakeFS" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"
RUN apk add -U --no-cache ca-certificates \
 && addgroup -S lakefs && adduser -S lakefs -G lakefs
WORKDIR /app
ENV PATH=/app:$PATH
COPY scripts/wait-for /app/
COPY --from=build /build/lakefs /build/lakectl /app/
COPY LICENSE NOTICE KAOS-FORK.md /app/
USER lakefs
WORKDIR /home/lakefs
EXPOSE 8000/tcp
ENTRYPOINT ["/app/lakefs"]
CMD ["run"]
