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

# Build Astro → static output to /astro/dist/
# `npm run build` runs `astro build` (clears dist/, builds pages).
# No separate bridge bundle: AppKit ships inside the WalletConnect island.
RUN npm run build

# ── Go builder ───────────────────────────────────────────────────────────────────
# Debian, not Alpine. The final stage is distroless/base-debian12 (glibc), and
# this stage builds with CGO_ENABLED=1 for the Zig bridge — so a musl-linked
# binary and musl-linked .so files would build fine here and then fail to exec
# in the final image, which has no /lib/ld-musl-x86_64.so.1. Both stages must
# agree on the libc. This image also ships gcc, curl and xz-utils already,
# which the alpine variant did not.
FROM golang:1.26-bookworm AS go-build
WORKDIR /src

# Copy go.mod files first (layer caching)
COPY backend/go.mod backend/go.sum ./backend/

# Download backend modules
RUN cd backend && go mod download

# Copy all source files
COPY backend/ ./backend/

# ── Install Zig compiler for zigmedia acceleration ──
# Download Zig 0.13.0 official release (Linux x86_64) — the same version
# used in CI (ci.yml). The tarball is ~47 MB and extracted to /usr/local.
# A symlink from /usr/local/bin/zig ensures zig is on PATH for build-lib.
# curl and xz-utils (for tar -xJf) ship with golang:bookworm, but install
# them explicitly so a future slimmer base image fails here with a clear
# message rather than midway through the Zig download.
RUN apt-get update && \
    apt-get install -y --no-install-recommends curl xz-utils && \
    rm -rf /var/lib/apt/lists/* && \
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
