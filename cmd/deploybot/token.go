package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/more-than-code/bot-mcp-server/internal/auth"
)

// tokenFile is the on-disk credential. api is the issuer base URL.
type tokenFile struct {
	API   string `json:"api"`
	Token string `json:"token"`
}

// loadedToken is a credential read back from disk.
// Legacy is true for an old plain-text file, which has no issuer.
type loadedToken struct {
	API    string
	Token  string
	Legacy bool
}

func tokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "deploybot", "token"), nil
}

// saveToken writes the issuer and token atomically.
// The directory is 0700 and the file is 0600.
func saveToken(apiURL, token string) error {
	if apiURL == "" || !auth.ValidToken(token) {
		return errors.New("token is empty or not a single header value")
	}
	body, err := marshalToken(tokenFile{API: apiURL, Token: token})
	if err != nil {
		return err
	}
	path, err := tokenPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	written := false
	defer func() {
		if !written {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	written = true
	return os.Chmod(path, 0o600)
}

func marshalToken(file tokenFile) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(file); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func loadToken() (loadedToken, error) {
	path, err := tokenPath()
	if err != nil {
		return loadedToken{}, err
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return loadedToken{}, err
	}
	trimmed := bytes.TrimSpace(buf)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.DisallowUnknownFields()
		var file tokenFile
		if err := dec.Decode(&file); err != nil {
			return loadedToken{}, errors.New("token file is empty or invalid")
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return loadedToken{}, errors.New("token file is empty or invalid")
		}
		if file.API == "" || !auth.ValidToken(file.Token) {
			return loadedToken{}, errors.New("token file is empty or invalid")
		}
		return loadedToken{API: file.API, Token: file.Token}, nil
	}
	token := string(buf)
	if !auth.ValidToken(token) {
		return loadedToken{}, errors.New("token file is empty or invalid")
	}
	return loadedToken{Token: token, Legacy: true}, nil
}

func deleteToken() error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// sameIssuer reports whether two base URLs name the same issuer.
// A trailing slash is ignored. Nothing else is rewritten.
func sameIssuer(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}
