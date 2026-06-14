// Command fin-mcp is the entrypoint for the Enable Banking CLI suite,
// exposing the MCP server, the interactive TUI dashboard, and the setup wizard.
package main

import "github.com/ngoldack/fin-mcp/internal/cli"

func main() {
	cli.Run()
}
