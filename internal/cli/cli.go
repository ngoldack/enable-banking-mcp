// Package cli wires the command-line interface using Kong's command pattern.
// Each subcommand is a struct with a Run() method, keeping main.go minimal and
// the commands independently testable.
package cli

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/mattn/go-isatty"

	"github.com/ngoldack/fin-mcp/internal/mcp"
	"github.com/ngoldack/fin-mcp/internal/setup"
	"github.com/ngoldack/fin-mcp/internal/tui"
)

// CLI is the root command tree parsed by Kong.
type CLI struct {
	Setup  SetupCmd  `cmd:"" help:"Configure credentials and authorize a bank connection (interactive TUI or via flags)."`
	Server ServerCmd `cmd:"" help:"Start the MCP server (stdio or SSE transport, configured via file and/or env)."`
	TUI    TUICmd    `cmd:"" name:"tui" help:"Start the interactive Terminal UI banking dashboard."`
}

// SetupCmd configures credentials and authorizes the bank connection.
type SetupCmd struct {
	Config      string `help:"Path to save the configuration file." default:"config.json" type:"path" short:"c"`
	AppID       string `help:"Enable Banking Application ID (UUID)."`
	PrivateKey  string `help:"Path to the RSA private key PEM file." type:"path" placeholder:"private.key"`
	Environment string `help:"API environment." default:"SANDBOX" enum:"SANDBOX,PRODUCTION"`
	RedirectURL string `help:"Application redirect URL." default:"http://localhost:8080/callback"`
	Country     string `help:"ISO 3166-1 alpha-2 country code of your bank (e.g. DE, FI)."`
	Bank        string `help:"Name of the bank (ASPSP)."`
	Code        string `help:"Authorization code from the bank redirect to complete setup."`
	Days        int    `help:"Consent validity in days." default:"90"`
}

// Run executes the setup command. It enforces an interactive TTY and chooses
// between flag-driven setup and the interactive TUI wizard.
func (c *SetupCmd) Run() error {
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("setup must run in an interactive terminal (TTY)")
	}

	// Any identifying flag triggers non-interactive (flag-driven) setup.
	if c.AppID != "" || c.Code != "" {
		return setup.RunFlagSetup(
			c.Config, c.AppID, c.PrivateKey, c.Environment,
			c.RedirectURL, c.Country, c.Bank, c.Code, c.Days,
		)
	}

	fmt.Fprintln(os.Stderr, "Launching interactive TUI Setup Wizard...")
	return tui.RunTUISetup(c.Config)
}

// ServerCmd starts the MCP server.
type ServerCmd struct {
	Config string `help:"Path to load the configuration file." default:"config.json" type:"path" short:"c"`
}

// Run starts the MCP server over the configured transport.
func (c *ServerCmd) Run() error {
	return mcp.RunMCPServer(c.Config)
}

// TUICmd starts the interactive dashboard.
type TUICmd struct {
	Config string `help:"Path to load the configuration file." default:"config.json" type:"path" short:"c"`
}

// Run starts the Bubble Tea dashboard.
func (c *TUICmd) Run() error {
	return tui.RunTUI(c.Config)
}

// Run parses arguments and dispatches to the selected command, exiting non-zero
// on failure (via Kong's FatalIfErrorf).
func Run() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("fin-mcp"),
		kong.Description("Enable Banking CLI Suite — reusable SDK, MCP server & TUI dashboard."),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}),
	)
	ctx.FatalIfErrorf(ctx.Run())
}
