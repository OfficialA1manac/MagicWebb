# Build
FROM golang:1.25-alpine AS build
# v23.6 — ARG moved to TOP of stage (immediately after FROM) so the
# value is part of the layer-cache key from the first RUN evaluation.
ARG GIT_SHA=unknown
WORKDIR /src

# Copy and download backend dependencies first (layer caching)
COPY backend/go.mod backend/go.sum ./backend/

# Download backend modules
RUN cd backend && go mod download

# Copy all source files
COPY backend/ ./backend/
COPY frontend/ ./frontend/

# v23.1 polish — audit-trail echo. Writes the SHA we are baking into
# the Fly builder log so an operator can `fly logs --app magicwebb`
# and SEE exactly what was stamped into MWServerBuildSHA.
RUN echo "Baking GIT_SHA=${GIT_SHA} into api.MWServerBuildSHA"
RUN cd backend && CGO_ENABLED=0 go build -ldflags="-s -w -X github.com/OfficialA1manac/MagicWebb/backend/internal/api.MWServerBuildSHA=${GIT_SHA}" -o /magicwebb ./cmd/server

# Run — UI templates, static assets and DB migrations are embedded in the binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /magicwebb /magicwebb
EXPOSE 8080
ENTRYPOINT ["/magicwebb"]
