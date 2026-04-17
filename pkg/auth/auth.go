package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
)

const callbackPath = "/oauth2/callback"

type Store struct {
	Path string
}

func TokenPath(configPath string) string {
	path := configPath
	if path == "" {
		path = "config.yml"
	}
	return path + ".token.json"
}

func (s Store) Load() (*oauth2.Token, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("token file %q does not exist; run `moon-shell --config=<config.yml> auth init`", s.Path)
		}
		return nil, fmt.Errorf("read token file %q: %w", s.Path, err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("decode token file %q: %w", s.Path, err)
	}
	if token.RefreshToken == "" {
		return nil, fmt.Errorf("token file %q does not contain a refresh_token; run auth init again", s.Path)
	}

	return &token, nil
}

func (s Store) Save(token *oauth2.Token) error {
	if token == nil {
		return errors.New("token is nil")
	}
	if token.RefreshToken == "" {
		return errors.New("token does not contain refresh_token")
	}

	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	if err := os.WriteFile(s.Path, data, 0o600); err != nil {
		return fmt.Errorf("write token file %q: %w", s.Path, err)
	}

	return nil
}

type InitResult struct {
	AuthURL   string
	TokenPath string
}

func Init(ctx context.Context, conf *oauth2.Config, store Store, onURL func(string)) (InitResult, error) {
	if conf == nil {
		return InitResult{}, errors.New("oauth config is nil")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return InitResult{}, fmt.Errorf("listen for oauth callback: %w", err)
	}
	defer listener.Close()

	confCopy := *conf
	confCopy.RedirectURL = "http://" + listener.Addr().String() + callbackPath

	state, err := randomString(32)
	if err != nil {
		return InitResult{}, fmt.Errorf("generate oauth state: %w", err)
	}
	verifier := oauth2.GenerateVerifier()

	type callbackResult struct {
		code string
		err  error
	}
	callbackCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		if errText := r.URL.Query().Get("error"); errText != "" {
			http.Error(w, "OAuth failed: "+errText, http.StatusBadRequest)
			select {
			case callbackCh <- callbackResult{err: fmt.Errorf("oauth authorization failed: %s", errText)}:
			default:
			}
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			select {
			case callbackCh <- callbackResult{err: errors.New("oauth state mismatch")}:
			default:
			}
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			select {
			case callbackCh <- callbackResult{err: errors.New("oauth callback missing authorization code")}:
			default:
			}
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Authorization complete. Return to the terminal.\n"))

		select {
		case callbackCh <- callbackResult{code: code}:
		default:
		}
	})

	serverErrCh := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
		close(serverErrCh)
	}()

	authURL := confCopy.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.S256ChallengeOption(verifier),
	)
	if onURL != nil {
		onURL(authURL)
	}

	result := InitResult{
		AuthURL:   authURL,
		TokenPath: store.Path,
	}

	select {
	case <-ctx.Done():
		_ = server.Shutdown(context.Background())
		return result, ctx.Err()
	case err := <-serverErrCh:
		if err != nil {
			return result, fmt.Errorf("oauth callback server failed: %w", err)
		}
	case callback := <-callbackCh:
		_ = server.Shutdown(context.Background())
		if callback.err != nil {
			return result, callback.err
		}

		token, err := confCopy.Exchange(ctx, callback.code, oauth2.VerifierOption(verifier))
		if err != nil {
			return result, fmt.Errorf("exchange oauth authorization code: %w", err)
		}
		if token.RefreshToken == "" {
			return result, errors.New("google did not return a refresh_token; revoke consent and retry auth init")
		}
		if err := store.Save(token); err != nil {
			return result, err
		}
		return result, nil
	}

	return result, nil
}

func randomString(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
