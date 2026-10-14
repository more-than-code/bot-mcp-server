package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const (
	minClientSecretBytes = 32
	maxClientSecretBytes = 4096
)

type config struct {
	Listen            string
	APIURL            string
	PublicOrigin      string
	AllowInsecureHTTP bool
	ClientSecret      string
}

func configFrom(getenv func(string) string) (config, error) {
	cfg := config{
		Listen:            strings.TrimSpace(getenv("BOT_MCP_LISTEN")),
		APIURL:            strings.TrimSpace(getenv("BOT_MCP_API_URL")),
		PublicOrigin:      strings.TrimSpace(getenv("BOT_MCP_PUBLIC_ORIGIN")),
		AllowInsecureHTTP: getenv("BOT_MCP_ALLOW_INSECURE_HTTP") == "1",
	}
	if cfg.Listen == "" || cfg.APIURL == "" || cfg.PublicOrigin == "" {
		return config{}, errors.New("BOT_MCP_LISTEN, BOT_MCP_API_URL, and BOT_MCP_PUBLIC_ORIGIN are required")
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return config{}, errors.New("BOT_MCP_LISTEN must be host:port")
	}
	if err := validateAPIURL(cfg.APIURL, cfg.AllowInsecureHTTP); err != nil {
		return config{}, err
	}
	if err := validatePublicOrigin(cfg.PublicOrigin, cfg.AllowInsecureHTTP); err != nil {
		return config{}, err
	}
	secret, err := readClientSecret(getenv("BOT_MCP_CLIENT_SECRET_FILE"))
	if err != nil {
		return config{}, err
	}
	cfg.ClientSecret = secret
	return cfg, nil
}

func validateAPIURL(raw string, allowInsecureHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("BOT_MCP_API_URL must be an absolute http(s) URL")
	}
	if u.Scheme == "http" && !apiclient.HTTPAllowed(u.Hostname(), allowInsecureHTTP) {
		return errors.New("BOT_MCP_API_URL must use https")
	}
	if u.User != nil {
		return errors.New("BOT_MCP_API_URL must not include user info")
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("BOT_MCP_API_URL must not include a path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("BOT_MCP_API_URL must not include a query or fragment")
	}
	return nil
}

func validatePublicOrigin(raw string, allowInsecureHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("BOT_MCP_PUBLIC_ORIGIN must be an absolute http(s) URL")
	}
	if u.Scheme == "http" && !apiclient.HTTPAllowed(u.Hostname(), allowInsecureHTTP) {
		return errors.New("BOT_MCP_PUBLIC_ORIGIN must use https")
	}
	if u.User != nil {
		return errors.New("BOT_MCP_PUBLIC_ORIGIN must not include user info")
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("BOT_MCP_PUBLIC_ORIGIN must not include a path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("BOT_MCP_PUBLIC_ORIGIN must not include a query or fragment")
	}
	if strings.ContainsAny(raw, "\"\\") {
		return fmt.Errorf("BOT_MCP_PUBLIC_ORIGIN contains a character that cannot be quoted")
	}
	return nil
}

// readClientSecret returns the first line of path.
// The file must be a regular file with mode 0600. The line terminator is
// removed. The line must be at least 32 bytes. The secret is never included
// in an error.
func readClientSecret(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE is missing")
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE is missing")
		}
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE could not be read")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE could not be read")
	}
	if !fi.Mode().IsRegular() {
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE must be a regular file")
	}
	if fi.Mode().Perm() != 0o600 {
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE must be mode 0600")
	}
	buf := make([]byte, maxClientSecretBytes+1)
	n, err := io.ReadFull(f, buf)
	if err == io.EOF || n == 0 {
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE is empty")
	}
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE could not be read")
	}
	buf = buf[:n]
	line := buf
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		line = buf[:i]
	} else if n > maxClientSecretBytes {
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE is invalid")
	}
	line = bytes.TrimRight(line, "\r")
	if len(line) == 0 {
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE is empty")
	}
	if len(line) < minClientSecretBytes {
		return "", errors.New("BOT_MCP_CLIENT_SECRET_FILE must be at least 32 characters")
	}
	return string(line), nil
}
