package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// ParamType specifies the expected type for a query parameter value.
type ParamType int

const (
	ParamString  ParamType = iota // any non-empty string
	ParamInt                      // integer (positive, negative, or zero)
	ParamWei                      // non-negative decimal integer string (wei value)
	ParamAddress                  // 0x-prefixed 42-char hex address
	ParamHex                      // any 0x-prefixed hex string
)

// QueryParam describes one allowed query parameter for a route.
type QueryParam struct {
	Name     string     // query parameter name (e.g. "collection")
	Required bool       // must be present and non-empty
	Type     ParamType  // expected value type
	OneOf    []string   // if non-nil, value must match one of these (case-sensitive)
}

// QuerySchema is a list of query parameters accepted by a route.
// Routes that accept no query params should use an empty QuerySchema{} to
// explicitly reject any query parameters (a nil schema skips validation).
type QuerySchema []QueryParam

// ValidateQuery returns a middleware that validates the request's query
// parameters against the given schema. It rejects:
//   - Unknown parameters (not in the schema) — catches typos like `?uri=` instead of `?url=`
//   - Missing required parameters
//   - Values that don't match the declared type
//   - Values that are not in the allowed set (OneOf)
//
// Pass a nil schema to skip validation entirely. Pass an empty (non-nil)
// schema (QuerySchema{}) to reject ALL query parameters.
func ValidateQuery(schema QuerySchema) fiber.Handler {
	// nil schema = skip validation entirely (opt-out for routes that don't
	// participate). Empty (non-nil) schema = reject ALL query params.
	if schema == nil {
		return func(c *fiber.Ctx) error { return c.Next() }
	}

	// Build fast lookup: param name → QueryParam
	allowed := make(map[string]QueryParam, len(schema))
	for _, p := range schema {
		allowed[p.Name] = p
	}

	return func(c *fiber.Ctx) error {
		// 1. Collect all query param names from the request
		var paramNames []string
		c.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
			if len(key) > 0 {
				paramNames = append(paramNames, string(key))
			}
		})

		// 2. Check for unknown parameters (not in the schema)
		for _, name := range paramNames {
			if _, ok := allowed[name]; !ok {
				return writeErr(c, fiber.StatusBadRequest,
					fmt.Sprintf("unknown query parameter: %s", name))
			}
		}

	// 3. Validate each declared parameter
	for _, p := range schema {
		val := c.Query(p.Name)
		// Determine if the param was present in the query string (even if empty).
		present := false
		c.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
			if string(key) == p.Name {
				present = true
			}
		})
		if val == "" {
			if present {
				return writeErr(c, fiber.StatusBadRequest,
					fmt.Sprintf("query parameter %s is present but empty", p.Name))
			}
			if p.Required {
				return writeErr(c, fiber.StatusBadRequest,
					fmt.Sprintf("required query parameter missing: %s", p.Name))
			}
			continue
		}

		if err := validateValue(p, val); err != nil {
			return writeErr(c, fiber.StatusBadRequest, err.Error())
		}
	}

		return c.Next()
	}
}

// validateValue checks a single parameter value against its schema.
func validateValue(p QueryParam, val string) error {
	switch p.Type {
	case ParamInt:
		if _, err := strconv.Atoi(val); err != nil {
			return fmt.Errorf("query parameter %s must be an integer, got %q", p.Name, val)
		}
	case ParamWei:
		if !isValidWeiStr(val) {
			return fmt.Errorf("query parameter %s must be a non-negative integer wei value, got %q", p.Name, val)
		}
	case ParamAddress:
		if !isValidHexAddress(val) {
			return fmt.Errorf("query parameter %s must be a valid 0x-prefixed address, got %q", p.Name, val)
		}
	case ParamHex:
		if !strings.HasPrefix(val, "0x") || len(val) < 3 {
			return fmt.Errorf("query parameter %s must be a 0x-prefixed hex string, got %q", p.Name, val)
		}
		for _, r := range val[2:] {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return fmt.Errorf("query parameter %s must be a valid hex string, got %q", p.Name, val)
			}
		}
	}

	// Validate OneOf constraint
	if len(p.OneOf) > 0 {
		valid := false
		for _, opt := range p.OneOf {
			if val == opt {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("query parameter %s must be one of [%s], got %q",
				p.Name, strings.Join(p.OneOf, ", "), val)
		}
	}

	return nil
}

// isValidHexAddress checks if s is a valid 0x-prefixed Ethereum address
// (42 characters, hex only).
func isValidHexAddress(s string) bool {
	if len(s) != 42 || !strings.HasPrefix(s, "0x") {
		return false
	}
	for _, r := range s[2:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
