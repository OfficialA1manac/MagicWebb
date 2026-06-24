# Build
FROM golang:1.25-alpine AS build
# v23.6 — ARG moved to TOP of stage (immediately after FROM) so the
# value is part of the layer-cache key from the first RUN evaluation.
# The earlier placement AFTER all COPYs (line 15) left ARG as a
# late-binding substitution; BuildKit computed the layer hash WITHOUT
# the ARG value, so a subsequent `fly deploy --build-arg
# GIT_SHA=<real>` was treated as a no-op on the cache and the binary
# shipped with `MWServerBuildSHA=unknown`. Placing the ARG declaration
# here pins it to the very first layer-key computation, so every new
# SHA forces a fresh build. Defaults to "unknown" so any deploy that
# forgets the arg still emits a loud-fail header.
ARG GIT_SHA=unknown
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# v23.6 — The `ARG GIT_SHA=unknown` declaration was moved to the top
# of this stage (immediately after FROM). It is referenced below via
# shell interpolation `${GIT_SHA}` in the build ldflags. The comment
# block here is kept as documentation of intent; the actual ARG line
# lives at the top so BuildKit's layer-cache key includes the value
# from the first RUN.
# v23.1 polish — audit-trail echo. Writes the SHA we are baking into
# the Fly builder log so an operator can `fly logs --app magicwebb`
# (or inspect the CI step output, since GitHub Actions surfaces the
# build log) and SEE exactly what was stamped into MWServerBuildSHA
# without having to curl /healthz. Cheap, defensive, and the only way
# to verify the SHA without a running machine — useful for
# post-mortem work after a `--remove-machine` event or a Fly regional
# outage. Goes BEFORE the go build so the echo is in the log even if
# the build itself fails (e.g. corrupted input, missing dep).
RUN echo "Baking GIT_SHA=${GIT_SHA} into api.MWServerBuildSHA"
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X github.com/OfficialA1manac/MagicWebb/backend/internal/api.MWServerBuildSHA=${GIT_SHA}" -o /magicwebb ./cmd/server

# Run — UI templates, static assets and DB migrations are embedded in the binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /magicwebb /magicwebb
EXPOSE 8080
ENTRYPOINT ["/magicwebb"]
