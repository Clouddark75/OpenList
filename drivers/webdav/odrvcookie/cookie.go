package odrvcookie

import (
	"fmt"
	"sync"
	"time"
)

// TokenResponse contiene el token de acceso OAuth2
type TokenResponse struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	RefreshToken string    `json:"refresh_token"`
	Scope        string    `json:"scope"`
	ExpiresAt    time.Time `json:"-"`
}

// IsExpired verifica si el token ha expirado (con 5 min de margen)
func (t *TokenResponse) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Add(5 * time.Minute).After(t.ExpiresAt)
}

// Cache global para tokens
var tokenCache = struct {
	sync.RWMutex
	tokens map[string]*TokenResponse
}{
	tokens: make(map[string]*TokenResponse),
}

// GetCookie obtiene el token de autenticación con caché automático
// Reemplaza la función original que retornaba cookies FedAuth/rtFa
// NOTA: Para cuentas universitarias con MFA, usa GetCookieInteractive() en su lugar
func GetCookie(username, password, siteUrl string) (string, error) {
	return GetCookieWithTenant(username, password, siteUrl, "")
}

// GetCookieWithTenant obtiene el token con tenant ID específico
func GetCookieWithTenant(username, password, siteUrl, tenantID string) (string, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s", username, siteUrl, tenantID)
	
	// Intentar obtener del caché
	tokenCache.RLock()
	cachedToken, exists := tokenCache.tokens[cacheKey]
	tokenCache.RUnlock()
	
	// Si existe y no ha expirado, usar el token en caché
	if exists && !cachedToken.IsExpired() {
		return fmt.Sprintf("Bearer %s", cachedToken.AccessToken), nil
	}
	
	// Si existe pero expiró, intentar refrescar
	if exists && cachedToken.RefreshToken != "" {
		ca := NewWithTenant(username, password, siteUrl, tenantID)
		newToken, err := ca.RefreshAccessToken(cachedToken.RefreshToken)
		if err == nil {
			// Refresh exitoso, actualizar caché
			tokenCache.Lock()
			tokenCache.tokens[cacheKey] = newToken
			tokenCache.Unlock()
			return fmt.Sprintf("Bearer %s", newToken.AccessToken), nil
		}
		// Si el refresh falló, continuar para obtener token nuevo
	}
	
	// Intentar ROPC primero (funciona si no hay MFA)
	ca := NewWithTenant(username, password, siteUrl, tenantID)
	token, err := ca.GetAccessToken()
	if err != nil {
		// ROPC falló (probablemente MFA o bloqueado por políticas)
		// Fallback a Device Flow
		return GetCookieInteractive(siteUrl, nil)
	}
	
	// Guardar en caché
	tokenCache.Lock()
	tokenCache.tokens[cacheKey] = token
	tokenCache.Unlock()
	
	return fmt.Sprintf("Bearer %s", token.AccessToken), nil
}

// ClearCache limpia el caché de tokens (útil para testing o logout)
func ClearCache() {
	tokenCache.Lock()
	tokenCache.tokens = make(map[string]*TokenResponse)
	tokenCache.Unlock()
}

// ClearUserCache limpia el token de un usuario específico
func ClearUserCache(username, siteUrl string) {
	cacheKey := fmt.Sprintf("%s:%s", username, siteUrl)
	tokenCache.Lock()
	delete(tokenCache.tokens, cacheKey)
	tokenCache.Unlock()
}
