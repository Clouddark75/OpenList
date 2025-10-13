package odrvcookie

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeviceCodeResponse contiene el código que el usuario debe ingresar
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

// GetCookieInteractive obtiene el token usando Device Code Flow
// Este método funciona incluso con MFA y sin permisos admin
func GetCookieInteractive(siteUrl string, onDeviceCode func(code, url string)) (string, error) {
	ca := CookieAuth{
		endpoint: siteUrl,
		tenantID: "organizations",
	}
	
	token, err := ca.GetAccessTokenDeviceFlow(onDeviceCode)
	if err != nil {
		return "", err
	}
	
	// Guardar en caché
	cacheKey := fmt.Sprintf("interactive:%s", siteUrl)
	tokenCache.Lock()
	tokenCache.tokens[cacheKey] = token
	tokenCache.Unlock()
	
	return fmt.Sprintf("Bearer %s", token.AccessToken), nil
}

// GetAccessTokenDeviceFlow implementa el flujo de código de dispositivo
func (ca *CookieAuth) GetAccessTokenDeviceFlow(onDeviceCode func(code, url string)) (*TokenResponse, error) {
	resource, err := ca.getResourceURL()
	if err != nil {
		return nil, err
	}

	// Paso 1: Solicitar código de dispositivo
	deviceCode, err := ca.requestDeviceCode(resource)
	if err != nil {
		return nil, err
	}

	// Paso 2: Mostrar código al usuario
	if onDeviceCode != nil {
		onDeviceCode(deviceCode.UserCode, deviceCode.VerificationURI)
	} else {
		fmt.Printf("\n🔐 Autenticación requerida:\n")
		fmt.Printf("   1. Ve a: %s\n", deviceCode.VerificationURI)
		fmt.Printf("   2. Ingresa el código: %s\n", deviceCode.UserCode)
		fmt.Printf("   3. Inicia sesión con tu cuenta universitaria\n\n")
	}

	// Paso 3: Polling - esperar a que el usuario complete la autenticación
	return ca.pollForToken(deviceCode)
}

// requestDeviceCode solicita un código de dispositivo
func (ca *CookieAuth) requestDeviceCode(resource string) (*DeviceCodeResponse, error) {
	deviceURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/devicecode", ca.tenantID)

	data := url.Values{}
	data.Set("client_id", "d3590ed6-52b3-4102-aeff-aad2292ab01c")
	data.Set("scope", resource+"/.default offline_access")

	req, err := http.NewRequest("POST", deviceURL, strings.NewReader(data.Encode()))
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
		return nil, fmt.Errorf("error solicitando código (status %d): %v", resp.StatusCode, errResp)
	}

	var deviceResp DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
		return nil, fmt.Errorf("error parseando respuesta: %w", err)
	}

	return &deviceResp, nil
}

// pollForToken hace polling hasta que el usuario complete la autenticación
func (ca *CookieAuth) pollForToken(deviceCode *DeviceCodeResponse) (*TokenResponse, error) {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", ca.tenantID)
	interval := time.Duration(deviceCode.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	timeout := time.After(time.Duration(deviceCode.ExpiresIn) * time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("timeout esperando autenticación del usuario")
		case <-ticker.C:
			data := url.Values{}
			data.Set("client_id", "d3590ed6-52b3-4102-aeff-aad2292ab01c")
			data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
			data.Set("device_code", deviceCode.DeviceCode)

			req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
			if err != nil {
				continue
			}

			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				continue
			}

			if resp.StatusCode == http.StatusOK {
				var tokenResp TokenResponse
				if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
					resp.Body.Close()
					continue
				}
				resp.Body.Close()

				tokenResp.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
				return &tokenResp, nil
			}

			// Leer el error
			var errResp map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&errResp)
			resp.Body.Close()

			if errCode, ok := errResp["error"].(string); ok {
				switch errCode {
				case "authorization_pending":
					// Usuario aún no completó la autenticación, continuar polling
					continue
				case "slow_down":
					// Reducir frecuencia de polling
					interval += 5 * time.Second
					ticker.Reset(interval)
					continue
				case "authorization_declined":
					return nil, fmt.Errorf("usuario rechazó la autorización")
				case "expired_token":
					return nil, fmt.Errorf("el código de dispositivo expiró")
				default:
					return nil, fmt.Errorf("error de autenticación: %s", errCode)
				}
			}
		}
	}
}
