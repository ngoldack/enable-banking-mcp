package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/mattn/go-isatty"
	"github.com/ngoldack/enable-banking-go/pkg/mcp"
	"github.com/ngoldack/enable-banking-go/pkg/setup"
	"github.com/ngoldack/enable-banking-go/pkg/tui"
)

type CLI struct {
	Setup struct {
		Config      string `help:"Path to save configuration file." default:"config.json" type:"path" short:"c"`
		AppID       string `help:"Enable Banking Application ID (UUID)."`
		PrivateKey  string `help:"Path to RSA private key PEM file." type:"path" placeholder:"private.key"`
		Environment string `help:"API environment (SANDBOX or PRODUCTION)." default:"SANDBOX" enum:"SANDBOX,PRODUCTION"`
		RedirectURL string `help:"Application redirect URL." default:"http://localhost:8080/callback"`
		Country     string `help:"ISO 2-letter country code of your bank (e.g. DE, FI)."`
		Bank        string `help:"Name of the bank (ASPSP)."`
		Code        string `help:"Authorization code from the bank redirect to complete the setup and exchange code."`
		Days        int    `help:"Consent validity in days." default:"90"`
	} `cmd:"" help:"Configure credentials and authorize bank connection (either via flags or interactively using a TUI)."`

	Server struct {
		Config string `help:"Path to load configuration file." default:"config.json" type:"path" short:"c"`
	} `cmd:"" help:"Start the MCP server over standard input/output (stdio)."`

	TUI struct {
		Config string `help:"Path to load configuration file." default:"config.json" type:"path" short:"c"`
	} `cmd:"" name:"tui" help:"Start the beautiful Terminal User Interface (TUI) Pocket Dashboard."`
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("enable-banking-go"),
		kong.Description("Enable Banking CLI Suite - Reusable SDK, MCP Server & Dashboard"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}),
	)

	switch ctx.Command() {
	case "setup":
		// Ensure setup is always run in an interactive TTY
		if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
			fmt.Fprintln(os.Stderr, "Error: Setup must always be interactive and run in a terminal (TTY)!")
			os.Exit(1)
		}

		// Check if any identifying flags are provided to run non-interactive setup
		if cli.Setup.AppID != "" || cli.Setup.Code != "" {
			err := setup.RunFlagSetup(
				cli.Setup.Config,
				cli.Setup.AppID,
				cli.Setup.PrivateKey,
				cli.Setup.Environment,
				cli.Setup.RedirectURL,
				cli.Setup.Country,
				cli.Setup.Bank,
				cli.Setup.Code,
				cli.Setup.Days,
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Flag setup failed: %v\n", err)
				os.Exit(1)
			}
		} else {
			// No flags provided -> launch beautiful TUI setup wizard!
			fmt.Println("Launching interactive TUI Setup Wizard...")
			err := tui.RunTUISetup(cli.Setup.Config)
			if err != nil {
				fmt.Fprintf(os.Stderr, "TUI setup failed: %v\n", err)
				os.Exit(1)
			}
		}
	case "server":
		err := mcp.RunMCPServer(cli.Server.Config)
		if err != nil {
			ctx.FatalIfErrorf(err)
		}
	case "tui":
		err := tui.RunTUI(cli.TUI.Config)
		if err != nil {
			ctx.FatalIfErrorf(err)
		}
	}
}
