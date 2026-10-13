package main

import (
	"io"
	"os"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil, nil))
}

func run(args []string, stdout, stderr io.Writer, sleep func(time.Duration), now func() time.Time) int {
	if sleep == nil {
		sleep = time.Sleep
	}
	if now == nil {
		now = time.Now
	}
	if len(args) == 0 {
		usage(stderr)
		return 1
	}
	switch args[0] {
	case "login":
		return runLogin(args[1:], stdout, stderr, sleep, now)
	case "logout":
		return runLogout(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stderr)
		return 0
	default:
		usage(stderr)
		return 1
	}
}

func usage(w io.Writer) {
	io.WriteString(w, `deploybot login [--api URL] [--client-name NAME] [--print] [--insecure-http]
deploybot logout [--api URL] [--force] [--insecure-http]

login runs the device-code flow against bot-api-server and saves
{"api","token"} to ~/.config/deploybot/token (mode 0600).
--api overrides DEPLOYBOT_API_URL.
--print writes the token to stdout instead of saving it.
--insecure-http allows an http URL that is not loopback.
logout calls /v1/auth/signout on the saved api. It deletes the file when
signout succeeds or returns unauthorized. --force deletes the file even if
signout fails.
Exit 2 means the device code expired. Exit 3 means the user denied it.
`)
}
