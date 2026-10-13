package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

type config struct {
	Listen            string
	APIURL            string
	PublicOrigin      string
	AllowInsecureHTTP bool
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
