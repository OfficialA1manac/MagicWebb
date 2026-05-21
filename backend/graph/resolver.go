package graph

import (
	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// Resolver is the root dependency container injected into all resolver types.
type Resolver struct {
	Q   *db.Q
	RDB *cache.Client
}
