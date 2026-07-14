# ── Astro (Node) builder ────────────────────────────────────────────────────────
FROM node:22-alpine AS astro-build
WORKDIR /astro

# Reown Project ID (shared with Go backend's WC_PROJECT_ID).
# Injected at build time so Astro's import.meta.env.PUBLIC_REOWN_PROJECT_ID
# is available in WalletConnect.tsx. Falls back to the same WC_PROJECT_ID
# used by the Go backend (fly.toml passes it as a build arg).
# REOWN_PROJECT_ID: MUST be passed via --build-arg (or via fly deploy --build-arg).
# Get yours at https://cloud.reown.com — it's a public client identifier.
ARG REOWN_PROJECT_ID
# Fail fast if REOWN_PROJECT_ID is not set at build time.
RUN test -n "$REOWN_PROJECT_ID" || (echo "FATAL: REOWN_PROJECT_ID build arg is required. Pass --build-arg REOWN_PROJECT_ID=..." && exit 1)
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

# ── Install Zig compiler for zigmedia acceleration ──
# Download Zig 0.13.0 official release (Linux x86_64) — the same version
# used in CI (ci.yml). The tarball is ~47 MB and extracted to /usr/local.
# A symlink from /usr/local/bin/zig ensures zig is on PATH for build-lib.
# xz-utils is required for tar -xJf; install it first since the golang
# alpine image doesn't include it by default.
RUN apk add --no-cache curl xz-utils && \
    curl -fsSL https://ziglang.org/download/0.13.0/zig-linux-x86_64-0.13.0.tar.xz -o /tmp/zig.tar.xz && \
    tar -xJf /tmp/zig.tar.xz -C /usr/local && \
    ln -sf /usr/local/zig-linux-x86_64-0.13.0/zig /usr/local/bin/zig && \
    zig version && \
    rm /tmp/zig.tar.xz

# ── Compile Zig shared libraries ──
# Compile each library with -O ReleaseFast for maximum performance.
# The output .so files (libzigsha256.so, libzigcrypto.so, libzignsniff.so)
# are placed in their respective directories. LDFLAGS in the Go CGO code
# reference them via -L${SRCDIR}/../../<lib> -l<lib>.
RUN cd backend/zigsha256 && zig build-lib -O ReleaseFast -dynamic zigsha256.zig && \
    cd ../zigcrypto && zig build-lib -O ReleaseFast -dynamic zigcrypto.zig && \
    cd ../zigsniff && zig build-lib -O ReleaseFast -dynamic zignsniff.zig

# ── Build Go binary with zigmedia acceleration ──
# CGO_ENABLED=1 is required for the #cgo LDFLAGS directives in the Zig
# bridge files. -tags zigmedia activates the CGO-backed implementations
# in hasher_zigmedia.go, zigcrypto.go, and zignsniff_zigmedia.go instead
# of the Go fallback defaults.
RUN cd backend && CGO_ENABLED=1 go build -tags zigmedia -ldflags="-s -w" -o /magicwebb ./cmd/server

# ── Final image (distroless/base includes libc for CGO/Zig .so support) ────────
FROM gcr.io/distroless/base-debian12:nonroot

# Go binary
COPY --from=go-build /magicwebb /magicwebb

# Zig-accelerated shared libraries for SHA-256, Keccak256, and image sniffing
# (compiled with zig build-lib -O ReleaseFast -dynamic). Copied to /usr/lib so
# the dynamic linker can find them at runtime via the default search path.
# Built with CGO_ENABLED=1 -tags zigmedia in the go-build stage.
COPY --from=go-build /src/backend/zigsha256/libzigsha256.so /usr/lib/
COPY --from=go-build /src/backend/zigcrypto/libzigcrypto.so /usr/lib/
COPY --from=go-build /src/backend/zigsniff/libzignsniff.so /usr/lib/

# Astro build output — served by Go at /app/* via ASTRO_DIST_DIR=/app/dist
COPY --from=astro-build /astro/dist /app/dist

ENV ASTRO_DIST_DIR=/app/dist

EXPOSE 8080
ENTRYPOINT ["/magicwebb"]
