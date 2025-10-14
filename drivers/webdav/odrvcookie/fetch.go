package odrvcookie

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CookieAuth estructura simple de autenticación
type CookieAuth struct {
	user     string
	pass     string
	endpoint string
	tenantID string
}

// TokenResponse respuesta del token OAuth2
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   string `json:"expires_in"` // Viene como string a veces
}

// GetExpiresInSeconds convierte expires_in a int
func (t *TokenResponse) GetExpiresInSeconds() int {
	if t.ExpiresIn == "" {
		return 3600 // Default 1 hora
	}
	// Intentar convertir a int
	var seconds int
	fmt.Sscanf(t.ExpiresIn, "%d", &seconds)
	if seconds <= 0 {
		return 3600
	}
	return seconds
}

// New crea una instancia de CookieAuth
func New(pUser, pPass, pEndpoint string) CookieAuth {
	return NewWithTenant(pUser, pPass, pEndpoint, "")
}

// NewWithTenant crea una instancia con tenant ID específico
func NewWithTenant(pUser, pPass, pEndpoint, pTenantID string) CookieAuth {
	tenantID := pTenantID
	if tenantID == "" {
		tenantID = "common" // Default
	}
	
	return CookieAuth{
		user:     pUser,
		pass:     pPass,
		endpoint: pEndpoint,
		tenantID: tenantID,
	}
}

// GetAccessToken obtiene el token usando ROPC con scope de Graph API
func (ca *CookieAuth) GetAccessToken() (*TokenResponse, error) {
	parsedURL, err := url.Parse(ca.endpoint)
	if err != nil {
		return nil, fmt.Errorf("URL inválida: %w", err)
	}
	
	hostname := parsedURL.Host
	
	// Usar tenant específico o common
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", ca.tenantID)
	
	// Client ID de Microsoft Office
	clientID := "d3590ed6-52b3-4102-aeff-aad2292ab01c"
	
	// Construir el scope correcto para SharePoint
	// Basado en el commit de rclone: usar el hostname de SharePoint como scope
	scope := fmt.Sprintf("https://%s/.default offline_access", hostname)
	
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("scope", scope)
	data.Set("username", ca.user)
	data.Set("password", ca.pass)
	data.Set("grant_type", "password")
	
	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error %d: %s", resp.StatusCode, string(body))
	}
	
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("error parseando token: %w", err)
	}
	
	return &tokenResp, nil
}
