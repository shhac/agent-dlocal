package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/mockdlocal"
)

func main() {
	var (
		addr      string
		routes    bool
		login     string
		transKey  string
		secretKey string
	)

	cmd := &cobra.Command{
		Use:   "mockdlocal",
		Short: "Local mock dLocal API server for agent-dlocal tests",
		Long:  "Local mock dLocal API server for agent-dlocal tests.\n\nRoutes:\n" + routeHelp(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if routes {
				for _, line := range mockdlocal.Routes() {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
						return err
					}
				}
				return nil
			}

			server := &http.Server{
				Addr: addr,
				Handler: mockdlocal.NewServerWithOptions(mockdlocal.Options{
					Login:     login,
					TransKey:  transKey,
					SecretKey: secretKey,
				}),
				ReadHeaderTimeout: 5 * time.Second,
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"status":   "listening",
				"base_url": "http://" + addr,
			})
			return server.ListenAndServe()
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&addr, "addr", "127.0.0.1:12112", "Address to listen on")
	flags.BoolVar(&routes, "routes", false, "Print the mock route map and exit")
	flags.StringVar(&login, "login", mockdlocal.DefaultLogin, "Expected X-Login")
	flags.StringVar(&transKey, "trans-key", mockdlocal.DefaultTransKey, "Expected X-Trans-Key")
	flags.StringVar(&secretKey, "secret-key", mockdlocal.DefaultSecretKey, "Secret used to verify signatures")

	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func routeHelp() string {
	out := ""
	for _, line := range mockdlocal.Routes() {
		out += "  " + line + "\n"
	}
	return out
}
