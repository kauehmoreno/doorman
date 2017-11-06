package doorman

import (
	"fmt"
	"net/http"
	"strings"

	auth0 "github.com/auth0-community/go-auth0"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	jose "gopkg.in/square/go-jose.v2"
	jwt "gopkg.in/square/go-jose.v2/jwt"
)

// PrincipalsContextKey is the Gin context key to obtain the current user principals.
const PrincipalsContextKey string = "principals"

// Claims is the set of information we extract from the JWT payload.
type Claims struct {
	Subject  string       `json:"sub,omitempty"`
	Audience jwt.Audience `json:"aud,omitempty"`
	Email    string       `json:"email,omitempty"`
	Groups   []string     `json:"groups,omitempty"`
}

// JWTValidator is the interface in charge of extracting JWT claims from request.
type JWTValidator interface {
	Initialize() error
	ExtractClaims(*http.Request) (*Claims, error)
}

// Auth0Validator is the implementation of JWTValidator for Auth0.
type Auth0Validator struct {
	Issuer    string
	validator *auth0.JWTValidator
}

// Initialize will fetch Auth0 public keys and instantiate a validator.
func (v *Auth0Validator) Initialize() error {
	if !strings.HasSuffix(v.Issuer, "auth0.com/") {
		return fmt.Errorf("issuer %q not supported or has bad format", v.Issuer)
	}
	jwksURI := fmt.Sprintf("%s.well-known/jwks.json", v.Issuer)
	log.Infof("JWT keys: %s", jwksURI)

	// Will check audience only when request comes in, leave empty for now.
	audience := []string{}

	client := auth0.NewJWKClient(auth0.JWKClientOptions{URI: jwksURI})
	config := auth0.NewConfiguration(client, audience, v.Issuer, jose.RS256)
	v.validator = auth0.NewValidator(config)
	return nil
}

// ExtractClaims validates the token from request, and returns the JWT claims.
func (v *Auth0Validator) ExtractClaims(request *http.Request) (*Claims, error) {
	token, err := v.validator.ValidateRequest(request)
	claims := Claims{}
	err = v.validator.Claims(request, token, &claims)
	if err != nil {
		return nil, err
	}
	return &claims, nil
}

// VerifyJWTMiddleware makes sure a valid JWT is provided.
func VerifyJWTMiddleware(validator JWTValidator) gin.HandlerFunc {
	validator.Initialize()

	return func(c *gin.Context) {
		claims, err := validator.ExtractClaims(c.Request)

		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"message": err.Error(),
			})
			return
		}

		// The service requesting must send its location. It will be compared
		// with the audiences defined in policies files.
		// XXX: The Origin request header might not be the best choice.
		origin := c.Request.Header.Get("Origin")
		if origin == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"message": "Missing `Origin` request header",
			})
			return
		}
		// Check that origin matches audiences from JWT token .
		if !claims.Audience.Contains(origin) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"message": "Invalid audience claim",
			})
			return
		}

		// Extract principals from JWT
		var principals Principals
		userid := fmt.Sprintf("userid:%s", claims.Subject)
		principals = append(principals, userid)
		// Main email (no alias)
		if claims.Email != "" {
			email := fmt.Sprintf("email:%s", claims.Email)
			principals = append(principals, email)
		}
		// Groups
		for _, group := range claims.Groups {
			prefixed := fmt.Sprintf("group:%s", group)
			principals = append(principals, prefixed)
		}

		c.Set(PrincipalsContextKey, principals)

		c.Next()
	}
}
