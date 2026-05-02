package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"skirk/internal/skirk"
)

type adcCredentials struct {
	Account      string `json:"account"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	Type         string `json:"type"`
}

func setup(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("setup needs init")
	}
	switch args[0] {
	case "init":
		return setupInit(ctx, args[1:])
	default:
		return fmt.Errorf("unknown setup command %q", args[0])
	}
}

func setupInit(ctx context.Context, args []string) error {
	defaultTitle := "skirk-" + time.Now().UTC().Format("20060102-150405")
	fs := flag.NewFlagSet("setup init", flag.ExitOnError)
	outDir := fs.String("out", "skirk-kit", "directory for generated configs")
	title := fs.String("title", defaultTitle, "Google workspace title prefix")
	sheet := fs.String("sheet", "skirk", "Google Sheet tab name")
	adcPath := fs.String("adc", "", "Application Default Credentials JSON path")
	noLogin := fs.Bool("no-gcloud-login", false, "fail instead of launching gcloud login if ADC is missing")
	clientRoute := fs.String("client-route", "google_front_pinned", "client Google API route: direct, real_pinned, google_front_pinned")
	exitRoute := fs.String("exit-route", "direct", "exit Google API route: direct, real_pinned, google_front_pinned")
	clientProxy := fs.String("client-proxy", "", "optional upstream SOCKS5 URL for the client")
	exitProxy := fs.String("exit-proxy", "", "optional upstream SOCKS5 URL for the exit")
	googleIP := fs.String("google-ip", "216.239.38.120", "Google edge IP for pinned routes")
	listen := fs.String("listen", "127.0.0.1:18080", "client SOCKS5 listen address")
	chunkSize := fs.Int("chunk-size", 1024*1024, "maximum tunnel chunk size")
	pollMS := fs.Int("poll-ms", 1200, "mailbox poll interval in milliseconds")
	clientConcurrency := fs.Int("client-concurrency", 1, "client Drive upload/download concurrency")
	exitConcurrency := fs.Int("exit-concurrency", 8, "exit Drive upload/download concurrency")
	if err := fs.Parse(args); err != nil {
		return err
	}

	credsPath := firstNonEmpty(*adcPath, defaultADCPath())
	creds, err := readADCCredentials(credsPath)
	if err != nil {
		if *noLogin {
			return fmt.Errorf("google ADC unavailable at %s: %w", credsPath, err)
		}
		fmt.Printf("Google login is required. Skirk will run gcloud and ask you to paste the browser code.\n\n")
		if err := runGcloudLogin(ctx); err != nil {
			return err
		}
		creds, err = readADCCredentials(credsPath)
		if err != nil {
			return fmt.Errorf("google ADC still unavailable at %s after login: %w", credsPath, err)
		}
	}
	if strings.TrimSpace(creds.Account) == "" {
		creds.Account = "unknown"
	}
	auth := creds.AuthConfig()
	adminCfg := skirk.Config{
		Secret: "setup-only",
		Auth:   auth,
		Route:  skirk.RouteConfig{Mode: "direct", GoogleIP: *googleIP, TimeoutSeconds: 240},
		Sheets: skirk.SheetsConfig{Range: *sheet + "!A:D"},
		Tunnel: skirk.TunnelConfig{ChunkSize: *chunkSize, PollIntervalMS: *pollMS, Concurrency: *exitConcurrency, CleanupProcessed: true},
	}
	adminCfg.ApplyDefaults()
	_, _, workspace, err := skirk.StoresFromConfig(ctx, &adminCfg)
	if err != nil {
		return err
	}

	spreadsheetID, err := workspace.CreateSpreadsheet(ctx, *title+" control", *sheet)
	if err != nil {
		return err
	}
	folderID, err := workspace.CreateDriveFolder(ctx, *title+" data")
	if err != nil {
		_ = workspace.DeleteSpreadsheet(ctx, spreadsheetID)
		return err
	}

	secret, err := skirk.RandomSecret()
	if err != nil {
		return err
	}
	session, err := skirk.NewSessionID()
	if err != nil {
		return err
	}
	sessionID := skirk.SessionString(session)
	baseDrive := skirk.DriveConfig{FolderID: folderID}
	baseSheets := skirk.SheetsConfig{SpreadsheetID: spreadsheetID, Range: *sheet + "!A:D"}
	clientCfg := skirk.Config{
		Secret:    secret,
		SessionID: sessionID,
		Auth:      auth,
		Route:     skirk.RouteConfig{Mode: *clientRoute, Proxy: *clientProxy, GoogleIP: *googleIP, TimeoutSeconds: 240},
		Drive:     baseDrive,
		Sheets:    baseSheets,
		Tunnel:    skirk.TunnelConfig{Listen: *listen, ChunkSize: *chunkSize, PollIntervalMS: *pollMS, Concurrency: *clientConcurrency, CleanupProcessed: true},
	}
	exitCfg := skirk.Config{
		Secret:    secret,
		SessionID: sessionID,
		Auth:      auth,
		Route:     skirk.RouteConfig{Mode: *exitRoute, Proxy: *exitProxy, GoogleIP: *googleIP, TimeoutSeconds: 240},
		Drive:     baseDrive,
		Sheets:    baseSheets,
		Tunnel:    skirk.TunnelConfig{Listen: *listen, ChunkSize: *chunkSize, PollIntervalMS: *pollMS, Concurrency: *exitConcurrency, CleanupProcessed: true},
	}
	if err := os.MkdirAll(*outDir, 0700); err != nil {
		return err
	}
	clientPath := filepath.Join(*outDir, "client.json")
	exitPath := filepath.Join(*outDir, "exit.json")
	readmePath := filepath.Join(*outDir, "README.md")
	if err := writeJSONFile(clientPath, clientCfg); err != nil {
		return err
	}
	if err := writeJSONFile(exitPath, exitCfg); err != nil {
		return err
	}
	if err := writeSetupReadme(readmePath, setupSummary{
		Title:         *title,
		ADCPath:       credsPath,
		Account:       creds.Account,
		ClientPath:    clientPath,
		ExitPath:      exitPath,
		SpreadsheetID: spreadsheetID,
		DriveFolderID: folderID,
		Listen:        *listen,
		ClientRoute:   *clientRoute,
		ExitRoute:     *exitRoute,
	}); err != nil {
		return err
	}

	return printJSON(map[string]any{
		"result":          "ok",
		"account":         creds.Account,
		"client_config":   clientPath,
		"exit_config":     exitPath,
		"readme":          readmePath,
		"spreadsheet_id":  spreadsheetID,
		"drive_folder_id": folderID,
		"client_route":    *clientRoute,
		"exit_route":      *exitRoute,
		"note":            "generated configs contain Google refresh credentials; treat them like passwords",
	})
}

func (c adcCredentials) AuthConfig() skirk.AuthConfig {
	return skirk.AuthConfig{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RefreshToken: c.RefreshToken,
		TokenURL:     "https://oauth2.googleapis.com/token",
	}
}

func readADCCredentials(path string) (adcCredentials, error) {
	if strings.TrimSpace(path) == "" {
		return adcCredentials{}, errors.New("empty ADC path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return adcCredentials{}, err
	}
	var creds adcCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return adcCredentials{}, err
	}
	if creds.Type != "" && creds.Type != "authorized_user" {
		return adcCredentials{}, fmt.Errorf("ADC type %q is not supported for one-file client configs; run user OAuth login", creds.Type)
	}
	if creds.ClientID == "" || creds.RefreshToken == "" {
		return adcCredentials{}, errors.New("ADC does not contain client_id and refresh_token")
	}
	return creds, nil
}

func defaultADCPath() string {
	if path := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")); path != "" {
		return path
	}
	if config := strings.TrimSpace(os.Getenv("CLOUDSDK_CONFIG")); config != "" {
		return filepath.Join(config, "application_default_credentials.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			return filepath.Join(appData, "gcloud", "application_default_credentials.json")
		}
	}
	return filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
}

func runGcloudLogin(ctx context.Context) error {
	gcloud, err := findGcloud()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, gcloud, "auth", "login", "--no-launch-browser", "--enable-gdrive-access", "--update-adc", "--force")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

func findGcloud() (string, error) {
	if path, err := exec.LookPath("gcloud"); err == nil {
		return path, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, "google-cloud-sdk", "bin", "gcloud")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("gcloud not found; install Google Cloud CLI or run setup with --adc /path/to/application_default_credentials.json")
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0600)
}

type setupSummary struct {
	Title         string
	ADCPath       string
	Account       string
	ClientPath    string
	ExitPath      string
	SpreadsheetID string
	DriveFolderID string
	Listen        string
	ClientRoute   string
	ExitRoute     string
}

func writeSetupReadme(path string, summary setupSummary) error {
	content := fmt.Sprintf(`# Skirk Generated Kit

Created workspace: %s

Google account: %s
ADC path: %s
Control spreadsheet: %s
Data folder: %s
Client route: %s
Exit route: %s

## What To Run

On the machine with normal internet egress, run the exit:

`+"```bash"+`
skirk serve-exit --config %s
`+"```"+`

On the client machine, run the SOCKS proxy:

`+"```bash"+`
skirk serve-client --config %s --listen %s
curl --socks5-hostname %s http://example.com/
`+"```"+`

## Config Handling

Send only `+"`client.json`"+` to client devices. Keep `+"`exit.json`"+` on the exit machine.

Both files contain Google refresh credentials and the Skirk tunnel secret. Treat them like passwords:

- do not commit them;
- do not paste them into logs or chats;
- regenerate the kit if one leaks.

## Cleanup / Disconnect

To delete the Google Sheet and Drive folder created by this kit:

`+"```bash"+`
skirk workspace delete --config %s --delete-drive-folder
`+"```"+`

To immediately invalidate every config generated from this OAuth login, revoke the app token from the Google account security page or run Google's OAuth revocation endpoint against the refresh token.

## Notes

The exit can be a VPS, a home server, or a laptop. It does not need an inbound port because both sides exchange encrypted chunks through Google Drive and Google Sheets. A VPS is still best for reliability because laptops sleep, move networks, and disappear when closed.
`, summary.Title, summary.Account, summary.ADCPath, summary.SpreadsheetID, summary.DriveFolderID, summary.ClientRoute, summary.ExitRoute, summary.ExitPath, summary.ClientPath, summary.Listen, summary.Listen, summary.ExitPath)
	return os.WriteFile(path, []byte(content), 0600)
}
