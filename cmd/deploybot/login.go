package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
	"github.com/more-than-code/bot-mcp-server/internal/auth"
)

const defaultClientName = "deploybot"

func runLogin(args []string, stdout, stderr io.Writer, sleep func(time.Duration), now func() time.Time) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiURL := fs.String("api", "", "bot-api-server base URL (overrides DEPLOYBOT_API_URL)")
	clientName := fs.String("client-name", defaultClientName, "device-flow client name, 1–100 characters")
	printToken := fs.Bool("print", false, "print the token instead of saving it")
	insecure := fs.Bool("insecure-http", false, "allow an http API URL that is not loopback")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected arguments")
		return 1
	}
	base := *apiURL
	if base == "" {
		base = os.Getenv("DEPLOYBOT_API_URL")
	}
	if base == "" {
		fmt.Fprintln(stderr, "set --api or DEPLOYBOT_API_URL")
		return 1
	}
	name := *clientName
	if len(name) < 1 || len(name) > 100 {
		fmt.Fprintln(stderr, "client name must be 1–100 characters")
		return 1
	}
	api, err := apiclient.New(base, *insecure)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	ctx := context.Background()
	code, err := api.RequestDeviceCode(ctx, name)
	if err != nil {
		fmt.Fprintln(stderr, loginErr(err))
		return 1
	}
	if code.DeviceCode == "" || code.UserCode == "" || code.VerificationURIComplete == "" {
		fmt.Fprintln(stderr, "device code response was incomplete")
		return 1
	}
	if code.ExpiresIn <= 0 || code.ExpiresIn > 24*60*60 || code.Interval < 0 || code.Interval > code.ExpiresIn {
		fmt.Fprintln(stderr, "device code response interval is invalid")
		return 1
	}
	fmt.Fprintf(stdout, "verification_uri_complete: %s\nuser_code: %s\n", code.VerificationURIComplete, code.UserCode)

	token, exit := pollDevice(ctx, api, code, sleep, now, stderr)
	if exit != 0 {
		return exit
	}
	if *printToken {
		fmt.Fprintln(stdout, token)
		return 0
	}
	if err := saveToken(base, token); err != nil {
		// The session exists. Drop it so a token that was never stored
		// cannot be used. Failure here still exits non-zero.
		_ = api.SignOut(ctx, token)
		fmt.Fprintln(stderr, "could not save token")
		return 1
	}
	fmt.Fprintln(stdout, "saved token to ~/.config/deploybot/token")
	return 0
}

func pollDevice(ctx context.Context, api *apiclient.Client, code apiclient.DeviceCode, sleep func(time.Duration), now func() time.Time, stderr io.Writer) (string, int) {
	interval := code.Interval
	if interval < 1 {
		interval = 1
	}
	deadline := now().Add(time.Duration(code.ExpiresIn) * time.Second)
	maxPolls := code.ExpiresIn + 5
	polls := 0
	for {
		sleep(time.Duration(interval) * time.Second)
		if !now().Before(deadline) {
			fmt.Fprintln(stderr, "expired_token: device code expired")
			return "", 2
		}
		if polls >= maxPolls {
			fmt.Fprintln(stderr, "expired_token: device code expired")
			return "", 2
		}
		polls++
		session, err := api.PollDeviceToken(ctx, code.DeviceCode)
		if err == nil {
			if !auth.ValidToken(session.Token) {
				fmt.Fprintln(stderr, "device token response was empty or invalid")
				return "", 1
			}
			return session.Token, 0
		}
		var apiErr *apiclient.Error
		if !errors.As(err, &apiErr) {
			fmt.Fprintln(stderr, "api request failed")
			return "", 1
		}
		switch apiErr.Code {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5
			continue
		case "expired_token":
			fmt.Fprintf(stderr, "expired_token: %s\n", apiErr.Msg)
			return "", 2
		case "access_denied":
			fmt.Fprintf(stderr, "access_denied: %s\n", apiErr.Msg)
			return "", 3
		default:
			fmt.Fprintf(stderr, "%s: %s\n", apiErr.Code, apiErr.Msg)
			return "", 1
		}
	}
}

func runLogout(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiURL := fs.String("api", "", "bot-api-server base URL (overrides DEPLOYBOT_API_URL)")
	force := fs.Bool("force", false, "delete the local token file even if signout fails")
	insecure := fs.Bool("insecure-http", false, "allow an http API URL that is not loopback")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected arguments")
		return 1
	}
	explicitAPI := *apiURL != ""
	given := *apiURL
	if given == "" {
		given = os.Getenv("DEPLOYBOT_API_URL")
	}
	cred, err := loadToken()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, "not logged in")
			return 1
		}
		fmt.Fprintln(stderr, err.Error())
		return finishLogout(false, *force, stdout, stderr)
	}
	target, ok := logoutTarget(cred, explicitAPI, *apiURL, given, stderr)
	if !ok {
		return finishLogout(false, *force, stdout, stderr)
	}
	api, err := apiclient.New(target, *insecure)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return finishLogout(false, *force, stdout, stderr)
	}
	signErr := api.SignOut(context.Background(), cred.Token)
	if signErr == nil {
		return finishLogout(true, true, stdout, stderr)
	}
	var apiErr *apiclient.Error
	if errors.As(signErr, &apiErr) && apiErr.Code == "unauthorized" {
		return finishLogout(true, true, stdout, stderr)
	}
	if *force {
		return finishLogout(true, true, stdout, stderr)
	}
	fmt.Fprintln(stderr, "signout failed; token kept, retry later or use --force")
	return 1
}

// logoutTarget is the issuer that may receive the token.
// The boolean is false when the token must not be sent.
func logoutTarget(cred loadedToken, explicitAPI bool, flagAPI, given string, stderr io.Writer) (string, bool) {
	if cred.Legacy {
		if !explicitAPI {
			fmt.Fprintln(stderr, "token file has no issuer; pass --api")
			return "", false
		}
		return flagAPI, true
	}
	if given != "" && !sameIssuer(given, cred.API) {
		fmt.Fprintln(stderr, "api url does not match the saved issuer")
		return "", false
	}
	return cred.API, true
}

// finishLogout deletes the file when remove is set and reports the result.
// remove is false when the file must stay. forceDelete deletes it anyway.
func finishLogout(remove, forceDelete bool, stdout, stderr io.Writer) int {
	if !remove && !forceDelete {
		return 1
	}
	if err := deleteToken(); err != nil {
		fmt.Fprintln(stderr, "could not remove token file")
		return 1
	}
	fmt.Fprintln(stdout, "logged out")
	return 0
}

func loginErr(err error) string {
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) {
		if apiErr.Msg == "" {
			return apiErr.Code
		}
		return apiErr.Code + ": " + apiErr.Msg
	}
	return "api request failed"
}
