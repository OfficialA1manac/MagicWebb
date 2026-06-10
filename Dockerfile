# Build
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /magicwebb ./cmd/server

# Run — UI templates, static assets and DB migrations are embedded in the binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /magicwebb /magicwebb
EXPOSE 8080
ENTRYPOINT ["/magicwebb"]
