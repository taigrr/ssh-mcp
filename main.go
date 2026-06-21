package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
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
// transport returns.
func run(allowedHosts []string) error {
	mgr := NewSSHManager(allowedHosts)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ssh-mcp",
		Version: version,
	}, nil)

	registerTools(server, mgr)

	return server.Run(context.Background(), &mcp.StdioTransport{})
}

func isExpectedShutdown(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return true
	}

	message := err.Error()
	return strings.Contains(message, "server is closing: EOF") || strings.Contains(message, "use of closed network connection") || strings.Contains(message, net.ErrClosed.Error())
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
// working directory. A missing config file is treated as an expected empty
// configuration so stdio clients do not see startup noise before the MCP
// handshake.
func loadAllowedHostsFromConfig() []string {
	jety.SetConfigFile(configFileName)
	_ = jety.SetConfigType(configFileType)
	if _, err := os.Stat(configFileName); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		log.Printf("Warning: could not stat %s: %v", configFileName, err)
		return nil
	}
	if err := jety.ReadInConfig(); err != nil {
		log.Printf("Warning: failed to read %s: %v", configFileName, err)
		return nil
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
			err := run(hosts)
			if isExpectedShutdown(err) {
				return nil
			}
			if err != nil {
				log.Printf("Server error: %v", err)
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&allowedHostsFlag, "allowed-hosts", "", "Comma-separated list of allowed SSH host aliases")

	if err := fang.Execute(context.Background(), cmd); err != nil {
		os.Exit(1)
	}
}
