package services

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TwitterOAuthService implements OAuthProvider and ContentPublisher for Twitter/X.
type TwitterOAuthService struct {
	cfg *config.Config
	*TokenHelper
	httpClient *http.Client
}

// NewTwitterOAuthService creates a new TwitterOAuthService.
func NewTwitterOAuthService(cfg *config.Config, tokenRepo *repository.TokenRepository) (*TwitterOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	return &TwitterOAuthService{
		cfg:         cfg,
		TokenHelper: NewTokenHelper(encryptor, tokenRepo),
		httpClient:  NewHTTPClient(),
	}, nil
}

func (s *TwitterOAuthService) GetPlatform() string { return models.PlatformTwitter }

func (s *TwitterOAuthService) GetLoginURL(state string) string {
	// Generate a cryptographically random PKCE code_verifier (64 bytes → 86 chars base64url)
	// and embed it in the state parameter so it survives the OAuth redirect round-trip.
	// Format: <original_state>.<verifier> — parsed back in HandleCallback via LastIndex.
	verifierBytes := make([]byte, 64)
	var verifier string
	if _, err := rand.Read(verifierBytes); err != nil {
		slog.Error("Twitter: failed to generate PKCE code_verifier, falling back to unsafe default", "error", err)
		verifier = "challenge"
	} else {
		verifier = base64.RawURLEncoding.EncodeToString(verifierBytes)
	}

	// Derive code_challenge via S256: SHA-256 hash of code_verifier, base64url-encoded.
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	params := url.Values{}
	params.Set("client_id", s.cfg.TwitterClientID)
	params.Set("redirect_uri", s.cfg.TwitterRedirectURI)
	params.Set("state", state+"."+verifier)
	params.Set("scope", "tweet.read tweet.write users.read offline.access")
	params.Set("response_type", "code")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")

	return "https://twitter.com/i/oauth2/authorize?" + params.Encode()
}

func (s *TwitterOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("Twitter: exchanging code for token")

	// Extract the PKCE code_verifier from the state parameter.
	// Format set in GetLoginURL: <original_state>.<verifier>
	verifier := ""
	if idx := strings.LastIndex(state, "."); idx != -1 {
		verifier = state[idx+1:]
	}
	if verifier == "" {
		return nil, nil, fmt.Errorf("twitter PKCE: missing code_verifier in state")
	}

	tokenResp, err := s.exchangeCodeForToken(ctx, code, verifier)
	if err != nil {
		return nil, nil, fmt.Errorf("twitter token exchange: %w", err)
	}

	slog.Info("Twitter: fetching user info")
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("twitter user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scopes:       strings.Split(tokenResp.Scope, " "),
	}

	return profile, tokenData, nil
}

// RefreshOAuthToken exchanges a Twitter refresh token for a new access token.
func (s *TwitterOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformTwitter, &err)
	if refreshToken == "" {
		return nil, fmt.Errorf("twitter RefreshOAuthToken: empty refresh token")
	}
	slog.Info("Twitter: refreshing access token")
	body := url.Values{}
	body.Set("client_id", s.cfg.TwitterClientID)
	body.Set("refresh_token", refreshToken)
	body.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.twitter.com/2/oauth2/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.cfg.TwitterClientID, s.cfg.TwitterClientSecret)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitter refresh request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("twitter refresh failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr twitterTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("twitter refresh parse: %w", err)
	}
	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = refreshToken
	}
	return &models.TokenData{
		AccessToken:  tr.AccessToken,
		RefreshToken: refresh,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tr.ExpiresIn,
		Scopes:       strings.Split(tr.Scope, " "),
	}, nil
}

func (s *TwitterOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformTwitter, time.Now(), &err)
	if payload.Text == "" && payload.ImageURL == "" {
		return nil, fmt.Errorf("twitter requires text or image_url")
	}

	slog.Info("Twitter: publishing tweet")

	tweetBody := map[string]interface{}{
		"text": payload.Text,
	}
	jsonBody, _ := json.Marshal(tweetBody)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.twitter.com/2/tweets",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("twitter tweet request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// When OAuth 1.0a static credentials are configured, sign the request
	// with OAuth 1.0a (user-context). Otherwise use the OAuth 2.0 Bearer
	// token from the database.
	if s.cfg.TwitterAccessToken != "" && s.cfg.TwitterAPIKey != "" {
		signOAuth1(req, s.cfg.TwitterAPIKey, s.cfg.TwitterAPIKeySecret,
			s.cfg.TwitterAccessToken, s.cfg.TwitterAccessTokenSecret)
	} else {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitter tweet failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("twitter tweet returned status %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Data struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("twitter tweet parse: %w", err)
	}

	return &models.PublishResult{
		PlatformMediaID: parsed.Data.ID,
		PlatformURL:     "https://twitter.com/i/status/" + parsed.Data.ID,
	}, nil
}

// --- Private ---

type twitterTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

func (s *TwitterOAuthService) exchangeCodeForToken(ctx context.Context, code, verifier string) (*twitterTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", s.cfg.TwitterClientID)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.TwitterRedirectURI)
	body.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.twitter.com/2/oauth2/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.cfg.TwitterClientID, s.cfg.TwitterClientSecret)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr twitterTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

// signOAuth1 adds an OAuth 1.0a Authorization header to the request using
// the static API key / access token credentials. Used as a fallback when
// OAuth 2.0 user authentication is not configured.
func signOAuth1(req *http.Request, consumerKey, consumerSecret, accessToken, accessTokenSecret string) {
	nonceBytes := make([]byte, 16)
	var nonce string
	if _, err := rand.Read(nonceBytes); err != nil {
		nonce = fmt.Sprintf("%d", time.Now().UnixNano())
	} else {
		nonce = fmt.Sprintf("%x", nonceBytes)
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())

	params := map[string]string{
		"oauth_consumer_key":     consumerKey,
		"oauth_nonce":            nonce,
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        timestamp,
		"oauth_token":            accessToken,
		"oauth_version":          "1.0",
	}

	// Build the signature base string.
	// Method & URL (percent-encoded).
	baseURL := fmt.Sprintf("%s://%s%s", req.URL.Scheme, req.URL.Host, req.URL.Path)
	encodedURL := url.QueryEscape(baseURL)

	// Collect and sort params.
	var paramPairs []string
	for k, v := range params {
		paramPairs = append(paramPairs, fmt.Sprintf("%s=%s",
			url.QueryEscape(k), url.QueryEscape(v)))
	}
	// Append query params from the URL.
	for k, vs := range req.URL.Query() {
		for _, v := range vs {
			paramPairs = append(paramPairs, fmt.Sprintf("%s=%s",
				url.QueryEscape(k), url.QueryEscape(v)))
		}
	}
	sort.Strings(paramPairs)
	paramString := strings.Join(paramPairs, "&")

	sigBase := fmt.Sprintf("%s&%s&%s",
		req.Method,
		encodedURL,
		url.QueryEscape(paramString),
	)

	// Signing key: consumer_secret & token_secret (raw values, NOT URL-encoded per RFC 5849).
	signingKey := consumerSecret + "&" + accessTokenSecret

	mac := hmac.New(sha1.New, []byte(signingKey))
	mac.Write([]byte(sigBase))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// Build Authorization header.
	params["oauth_signature"] = signature
	var authParts []string
	for k, v := range params {
		authParts = append(authParts, fmt.Sprintf(`%s="%s"`,
			url.QueryEscape(k), url.QueryEscape(v)))
	}
	req.Header.Set("Authorization", "OAuth "+strings.Join(authParts, ", "))
}

func (s *TwitterOAuthService) getUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.twitter.com/2/users/me?user.fields=id,name,username", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user info failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return &models.PlatformProfile{
		PlatformUserID: result.Data.ID,
		Username:       result.Data.Username,
		Name:           result.Data.Name,
	}, nil
}
