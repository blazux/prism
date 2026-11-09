package server

// The Connector REST protocol lets the Go server host a Teams bot without a
// second agent or SDK runtime. Verify Connector JWTs before trusting any activity
// field (including the tenant, sender and callback URL).
import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const teamsOpenIDURL = "https://login.botframework.com/v1/.well-known/openidconfiguration"

var teamsHTTP = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type teamsSigningKey struct {
	key      *rsa.PublicKey
	endorsed bool
}

type teamsAuthenticator struct {
	mu        sync.Mutex
	keys      map[string]teamsSigningKey
	fetched   time.Time
	attempted time.Time
}

func teamsReadJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := teamsHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Microsoft metadata returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (a *teamsAuthenticator) signingKey(ctx context.Context, kid string) (teamsSigningKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key, found := a.keys[kid]
	if time.Since(a.fetched) < 6*time.Hour && found {
		return key, nil
	}
	// Unknown key IDs must not turn the public endpoint into a metadata flood.
	if time.Since(a.fetched) < time.Minute || time.Since(a.attempted) < time.Minute {
		return teamsSigningKey{}, fmt.Errorf("unknown signing key")
	}
	a.attempted = time.Now()
	var metadata struct {
		Issuer     string   `json:"issuer"`
		KeysURL    string   `json:"jwks_uri"`
		Algorithms []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := teamsReadJSON(ctx, teamsOpenIDURL, &metadata); err != nil {
		return teamsSigningKey{}, err
	}
	u, err := url.Parse(metadata.KeysURL)
	if err != nil || u.Scheme != "https" || u.Host != "login.botframework.com" || u.User != nil || !contains(metadata.Algorithms, "RS256") || metadata.Issuer != "https://api.botframework.com" {
		return teamsSigningKey{}, fmt.Errorf("invalid Connector metadata")
	}
	var jwks struct {
		Keys []struct {
			ID           string   `json:"kid"`
			Type         string   `json:"kty"`
			Use          string   `json:"use"`
			N            string   `json:"n"`
			E            string   `json:"e"`
			Endorsements []string `json:"endorsements"`
		} `json:"keys"`
	}
	if err := teamsReadJSON(ctx, metadata.KeysURL, &jwks); err != nil {
		return teamsSigningKey{}, err
	}
	keys := make(map[string]teamsSigningKey)
	for _, k := range jwks.Keys {
		n, ne := base64.RawURLEncoding.DecodeString(k.N)
		e, ee := base64.RawURLEncoding.DecodeString(k.E)
		if k.Type != "RSA" || k.Use != "sig" || ne != nil || ee != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		modulus := new(big.Int).SetBytes(n)
		exponent := new(big.Int).SetBytes(e).Int64()
		if modulus.BitLen() < 2048 || exponent < 3 || exponent > 1<<31-1 || exponent%2 == 0 {
			continue
		}
		keys[k.ID] = teamsSigningKey{key: &rsa.PublicKey{N: modulus, E: int(exponent)}, endorsed: contains(k.Endorsements, "msteams")}
	}
	a.keys, a.fetched = keys, time.Now()
	key, found = keys[kid]
	if !found {
		return teamsSigningKey{}, fmt.Errorf("unknown signing key")
	}
	return key, nil
}

func (a *teamsAuthenticator) verify(ctx context.Context, authorization, appID, serviceURL string) error {
	fields := strings.Fields(authorization)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) > 16384 {
		return fmt.Errorf("Connector bearer token required")
	}
	parts := strings.Split(fields[1], ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid Connector JWT")
	}
	var header struct {
		Algorithm string   `json:"alg"`
		KeyID     string   `json:"kid"`
		Critical  []string `json:"crit"`
	}
	var claims struct {
		Issuer     string `json:"iss"`
		Audience   string `json:"aud"`
		Expires    int64  `json:"exp"`
		NotBefore  int64  `json:"nbf"`
		ServiceURL string `json:"serviceurl"`
	}
	h, he := base64.RawURLEncoding.DecodeString(parts[0])
	c, ce := base64.RawURLEncoding.DecodeString(parts[1])
	sig, se := base64.RawURLEncoding.DecodeString(parts[2])
	if he != nil || ce != nil || se != nil || json.Unmarshal(h, &header) != nil || json.Unmarshal(c, &claims) != nil || header.Algorithm != "RS256" || header.KeyID == "" || len(header.Critical) > 0 {
		return fmt.Errorf("invalid Connector JWT")
	}
	now := time.Now().Unix()
	if claims.Issuer != "https://api.botframework.com" || claims.Audience != appID || claims.Expires == 0 || claims.NotBefore == 0 || claims.Expires < now-300 || claims.NotBefore > now+300 || strings.TrimRight(claims.ServiceURL, "/") != strings.TrimRight(serviceURL, "/") || !validTeamsServiceURL(serviceURL) {
		return fmt.Errorf("invalid Connector claims")
	}
	key, err := a.signingKey(ctx, header.KeyID)
	if err != nil {
		return err
	}
	if !key.endorsed {
		return fmt.Errorf("signing key is not endorsed for Teams")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return rsa.VerifyPKCS1v15(key.key, crypto.SHA256, digest[:], sig)
}

// Restrict authenticated callbacks too: tokens must never be sent to a tenant's
// arbitrary URL. Sovereign-cloud Connector endpoints need their own issuer/keys.
func validTeamsServiceURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if u.Host != "smba.trafficmanager.net" && u.Host != "smba.infra.teams.microsoft.com" {
		return false
	}
	return !strings.Contains(u.EscapedPath(), "%") && !strings.Contains(u.Path, "..") && !strings.Contains(u.Path, "//")
}
