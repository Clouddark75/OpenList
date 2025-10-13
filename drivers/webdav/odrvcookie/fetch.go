package odrvcookie

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CookieAuth mantiene la información de autenticación
type CookieAuth struct {
	user     string
	pass     string
	endpoint string
	tenantID string
}

// New crea una nueva estructura CookieAuth
func New(pUser, pPass, pEndpoint string) CookieAuth {
	return CookieAuth{
		user:     pUser,
		pass:     pPass,
		endpoint: pEndpoint,
		tenantID: "common", // Usa "common" para multi-tenant
	}
}

// GetAccessToken obtiene un token OAuth2 usando el flujo ROPC
func (ca *CookieAuth) GetAccessToken() (*TokenResponse, error) {
	resource, err := ca.getResourceURL()
	if err != nil {
		return nil, err
	}

	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", ca.tenantID)

	data := url.Values{}
	data.Set("client_id", "d3590ed6-52b3-4102-aeff-aad2292ab01c") // Microsoft Office client ID
	data.Set("scope", resource+"/.default openid profile offline_access")
	data.Set("username", ca.user)
	data.Set("password", ca.pass)
	data.Set("grant_type", "password")

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("error creando request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error ejecutando request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("error de autenticación (status %d): %v", resp.StatusCode, errResp)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("error parseando respuesta: %w", err)
	}

	// Calcular tiempo de expiración
	tokenResp.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return &tokenResp, nil
}

// RefreshAccessToken refresca un token usando el refresh_token
func (ca *CookieAuth) RefreshAccessToken(refreshToken string) (*TokenResponse, error) {
	resource, err := ca.getResourceURL()
	if err != nil {
		return nil, err
	}

	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", ca.tenantID)

	data := url.Values{}
	data.Set("client_id", "d3590ed6-52b3-4102-aeff-aad2292ab01c")
	data.Set("scope", resource+"/.default openid profile offline_access")
	data.Set("refresh_token", refreshToken)
	data.Set("grant_type", "refresh_token")

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("error creando request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error ejecutando request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("error refrescando token (status %d): %v", resp.StatusCode, errResp)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("error parseando respuesta: %w", err)
	}

	tokenResp.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return &tokenResp, nil
}

// getResourceURL determina el recurso correcto basado en el endpoint
func (ca *CookieAuth) getResourceURL() (string, error) {
	parsedURL, err := url.Parse(ca.endpoint)
	if err != nil {
		return "", fmt.Errorf("error parseando endpoint: %w", err)
	}

	host := parsedURL.Host
	
	if strings.HasSuffix(host, ".sharepoint.com") {
		return "https://" + strings.Split(host, ".")[0] + ".sharepoint.com", nil
	} else if strings.HasSuffix(host, ".sharepoint.cn") {
		return "https://" + strings.Split(host, ".")[0] + ".sharepoint.cn", nil
	} else if strings.HasSuffix(host, ".sharepoint.us") {
		return "https://" + strings.Split(host, ".")[0] + ".sharepoint.us", nil
	} else if strings.HasSuffix(host, ".sharepoint.de") {
		return "https://" + strings.Split(host, ".")[0] + ".sharepoint.de", nil
	}

	return "https://" + host, nil
}

// GetGraphToken obtiene token para Microsoft Graph API
func (ca *CookieAuth) GetGraphToken() (*TokenResponse, error) {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", ca.tenantID)

	data := url.Values{}
	data.Set("client_id", "d3590ed6-52b3-4102-aeff-aad2292ab01c")
	data.Set("scope", "https://graph.microsoft.com/.default")
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

	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		return nil, fmt.Errorf("error status %d: %s", resp.StatusCode, buf.String())
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	tokenResp.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return &tokenResp, nil
}
