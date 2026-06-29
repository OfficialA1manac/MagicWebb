# ── Astro (Node) builder ────────────────────────────────────────────────────────
FROM node:22-alpine AS astro-build
WORKDIR /astro

# Reown Project ID (shared with Go backend's WC_PROJECT_ID).
# Injected at build time so Astro's import.meta.env.PUBLIC_REOWN_PROJECT_ID
# is available in WalletConnect.tsx. Falls back to the same WC_PROJECT_ID
# used by the Go backend (fly.toml passes it as a build arg).
ARG REOWN_PROJECT_ID=af6aba4c71274871c3d77a60050171ba
ENV PUBLIC_REOWN_PROJECT_ID=$REOWN_PROJECT_ID

# Copy app directory (Astro + Svelte + React + AppKit)
COPY app/package.json app/package-lock.json* ./

# Install deps; --legacy-peer-deps needed for @reown/appkit peer conflicts
RUN npm install --legacy-peer-deps

# Copy source files
COPY app/ ./

# Build Astro + AppKit bridge → static output to /astro/dist/
# `npm run build` runs `astro build` first (clears dist/, builds pages),
# then `npm run build:bridge` (appends the self-hosted AppKit bundle to
# dist/static/). Astro clears dist/ on every build, so the bridge MUST be
# built after Astro. The Go stage copies the bridge from here (next stage).
RUN npm run build

# ── Go builder ───────────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS go-build
WORKDIR /src

# Copy go.mod files first (layer caching)
COPY backend/go.mod backend/go.sum ./backend/
COPY frontend/go.mod ./frontend/

# Download backend modules (frontend has zero external deps)
RUN cd backend && go mod download

# Copy all source files
COPY backend/ ./backend/
COPY frontend/ ./frontend/

# ── Wire self-hosted AppKit bridge from Astro stage ──
# The bridge is built by `npm run build:bridge` (from the astro-build stage)
# and output to /astro/dist/static/appkit-bridge.js. The Go embed
# (frontend/embed.go) expects it at frontend/static/appkit-bridge.js, so we
# copy it here BEFORE go build. If the file doesn't exist (bridge build
# failed), this COPY fails the build — we WANT a hard failure because the
# self-hosted bridge is required for wallet pairing on the HTMX pages.
COPY --from=astro-build /astro/dist/static/appkit-bridge.js ./frontend/static/

RUN cd backend && CGO_ENABLED=0 go build -ldflags="-s -w" -o /magicwebb ./cmd/server

# ── Final image ───────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

# Go binary
COPY --from=go-build /magicwebb /magicwebb

# Astro build output — served by Go at /app/* via ASTRO_DIST_DIR=/app/dist
COPY --from=astro-build /astro/dist /app/dist

ENV ASTRO_DIST_DIR=/app/dist

EXPOSE 8080
ENTRYPOINT ["/magicwebb"]
