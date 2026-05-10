package main

import (
	"context"
	"log"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/webbplace/backend/internal/auth"
)

func main() {
	_ = godotenv.Load("../.env")

	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	v := &auth.Verifier{
		Domain:    envOr("SIWE_DOMAIN", "localhost:5173"),
		JWTSecret: []byte(os.Getenv("JWT_SECRET")),
	}

	app := fiber.New(fiber.Config{AppName: "WebbPlace API"})
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "http://localhost:5173",
		AllowCredentials: true,
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization",
	}))
	app.Use(v.Middleware())

	app.Get("/healthz", func(c *fiber.Ctx) error { return c.SendString("ok") })

	// SIWE login: client posts {message, signature}; we verify + issue JWT.
	app.Post("/auth/siwe", func(c *fiber.Ctx) error {
		var body struct{ Message, Signature string }
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		addr, tok, err := v.VerifyAndIssue(body.Message, body.Signature)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, err.Error())
		}
		// auto-create user row
		_, _ = pool.Exec(c.Context(),
			`INSERT INTO users(address) VALUES ($1) ON CONFLICT (address) DO NOTHING`, addr)
		return c.JSON(fiber.Map{"address": addr, "token": tok})
	})

	// GraphQL endpoint stub — wired up after `make codegen-graphql` regenerates handler.
	app.Post("/graphql", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusNotImplemented).
			SendString("run `make codegen-graphql` to generate gqlgen handler, then wire it here")
	})

	addr := envOr("HTTP_LISTEN", ":8080")
	log.Printf("api listening on %s", addr)
	log.Fatal(app.Listen(addr))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
