package main

import (
	"context"
	"log"
	"os"
	"strings"

	"charm.land/fang/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/taigrr/jety"
)

const (
	configFileName = "config.json"
	configFileType = "json"
)

// run boots the MCP server with the given allowlist and serves until the
// transport returns. It exits the process on transport errors.
func run(allowedHosts []string) {
	mgr := NewSSHManager(allowedHosts)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ssh-mcp",
		Version: version,
	}, nil)

	registerTools(server, mgr)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// parseAllowedHostsFlag splits a comma-separated --allowed-hosts value into
// trimmed, non-empty entries.
func parseAllowedHostsFlag(value string) []string {
	if value == "" {
		return nil
	}
	var hosts []string
	for host := range strings.SplitSeq(value, ",") {
		host = strings.TrimSpace(host)
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// loadAllowedHostsFromConfig reads the host list from config.json in the
// working directory. Missing config is logged but not fatal.
func loadAllowedHostsFromConfig() []string {
	jety.SetConfigFile(configFileName)
	_ = jety.SetConfigType(configFileType)
	if err := jety.ReadInConfig(); err != nil {
		log.Printf("Warning: config file not found: %v", err)
	}
	return loadAllowedHosts()
}

func main() {
	var allowedHostsFlag string

	cmd := &cobra.Command{
		Use:   "ssh-mcp",
		Short: "SSH MCP server providing remote shell access via Model Context Protocol",
		RunE: func(c *cobra.Command, args []string) error {
			hosts := parseAllowedHostsFlag(allowedHostsFlag)
			if hosts == nil {
				hosts = loadAllowedHostsFromConfig()
			}
			run(hosts)
			return nil
		},
	}

	cmd.Flags().StringVar(&allowedHostsFlag, "allowed-hosts", "", "Comma-separated list of allowed SSH host aliases")

	if err := fang.Execute(context.Background(), cmd); err != nil {
		os.Exit(1)
	}
}
