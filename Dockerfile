# ── Astro (Node) builder ────────────────────────────────────────────────────────
FROM node:22-alpine AS astro-build
WORKDIR /astro

# Copy app directory (Astro + Svelte + React + AppKit)
COPY app/package.json app/package-lock.json* ./

# Install deps; --legacy-peer-deps needed for @reown/appkit peer conflicts
RUN npm install --legacy-peer-deps

# Copy source files
COPY app/ ./

# Build Astro → static output to /astro/dist/
RUN npx astro build

# ── Go builder ───────────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS go-build
ARG GIT_SHA=unknown
WORKDIR /src

# Copy go.mod files first (layer caching)
COPY backend/go.mod backend/go.sum ./backend/
COPY frontend/go.mod ./frontend/

# Download backend modules (frontend has zero external deps)
RUN cd backend && go mod download

# Copy all source files
COPY backend/ ./backend/
COPY frontend/ ./frontend/

RUN echo "Baking GIT_SHA=${GIT_SHA} into api.MWServerBuildSHA"
RUN cd backend && CGO_ENABLED=0 go build -ldflags="-s -w -X github.com/OfficialA1manac/MagicWebb/backend/internal/api.MWServerBuildSHA=${GIT_SHA}" -o /magicwebb ./cmd/server

# ── Final image ───────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

# Go binary
COPY --from=go-build /magicwebb /magicwebb

# Astro build output — served by Go at /app/* via ASTRO_DIST_DIR=/app/dist
COPY --from=astro-build /astro/dist /app/dist

ENV ASTRO_DIST_DIR=/app/dist

EXPOSE 8080
ENTRYPOINT ["/magicwebb"]
