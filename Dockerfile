# Build
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# v23.1 — Inject git SHA via ARG+ldflags as api.MWServerBuildSHA so /healthz
#         serves an X-MW-Build-SHA header that tools/check-fly-sync.sh
#         reads against origin/main. Pass via `--build-arg GIT_SHA=<sha>`
#         from CI deploys (`fly deploy --build-arg`) and from local
#         Makefile-driven Docker builds. Defaults to "unknown" so any
#         deploy that forgets the arg still emits a header — which fails
#         the sync gate immediately. That loud-fail intent is the v74-class
#         deploy-drift fix in action: silent drift is not an option.
ARG GIT_SHA=unknown
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X github.com/OfficialA1manac/MagicWebb/backend/internal/api.MWServerBuildSHA=${GIT_SHA}" -o /magicwebb ./cmd/server

# Run — UI templates, static assets and DB migrations are embedded in the binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /magicwebb /magicwebb
EXPOSE 8080
ENTRYPOINT ["/magicwebb"]
