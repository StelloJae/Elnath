package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/providerproxy"
)

type proxyProviderView struct {
	Provider         string   `json:"provider"`
	DisplayName      string   `json:"display_name"`
	Ready            bool     `json:"ready"`
	Authenticated    bool     `json:"authenticated"`
	APIKeyConfigured bool     `json:"api_key_configured"`
	BaseURL          string   `json:"base_url,omitempty"`
	AllowedPaths     []string `json:"allowed_paths"`
	Refreshable      bool     `json:"refreshable"`
	AuthFailure      string   `json:"auth_failure,omitempty"`
}

type proxyStatusView struct {
	State              string            `json:"state"`
	Host               string            `json:"host"`
	Port               int               `json:"port"`
	ListenURL          string            `json:"listen_url"`
	Provider           string            `json:"provider"`
	DisplayName        string            `json:"display_name"`
	Ready              bool              `json:"ready"`
	Authenticated      bool              `json:"authenticated"`
	AllowedPaths       []string          `json:"allowed_paths"`
	LANExposureWarning string            `json:"lan_exposure_warning,omitempty"`
	ProviderStatus     proxyProviderView `json:"provider_status"`
}

func cmdProxy(ctx context.Context, args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	switch args[0] {
	case "status":
		return proxyStatus(ctx, args[1:])
	case "providers":
		return proxyProviders(ctx, args[1:])
	case "start":
		return proxyStart(ctx, args[1:])
	case "help", "-h", "--help":
		return proxyUsage()
	default:
		return fmt.Errorf("proxy: unknown subcommand %q (try: elnath proxy help)", args[0])
	}
}

func proxyUsage() error {
	fmt.Fprintln(os.Stdout, "Usage: elnath proxy [status [--json]|providers [--json]|start [--host HOST] [--port PORT]]")
	return nil
}

func proxyStatus(ctx context.Context, args []string) error {
	opts, err := parseProxyOptions(args)
	if err != nil {
		return fmt.Errorf("proxy status: %w", err)
	}
	cfg, err := loadProxyCommandConfig()
	if err != nil {
		return fmt.Errorf("proxy status: %w", err)
	}
	adapter, err := providerproxy.OpenAIResponsesAdapterFromConfig(cfg)
	if err != nil {
		return fmt.Errorf("proxy status: %w", err)
	}
	view := proxyStatusViewForAdapter(ctx, adapter, opts, "stopped")
	if opts.JSON {
		return json.NewEncoder(os.Stdout).Encode(view)
	}
	fmt.Fprintf(os.Stdout, "Proxy: %s\n", view.State)
	fmt.Fprintf(os.Stdout, "Listen URL: %s\n", view.ListenURL)
	fmt.Fprintf(os.Stdout, "Provider: %s\n", view.Provider)
	fmt.Fprintf(os.Stdout, "Ready: %t\n", view.Ready)
	if view.LANExposureWarning != "" {
		fmt.Fprintln(os.Stderr, view.LANExposureWarning)
	}
	return nil
}

func proxyProviders(ctx context.Context, args []string) error {
	opts, err := parseProxyOptions(args)
	if err != nil {
		return fmt.Errorf("proxy providers: %w", err)
	}
	cfg, err := loadProxyCommandConfig()
	if err != nil {
		return fmt.Errorf("proxy providers: %w", err)
	}
	adapter, err := providerproxy.OpenAIResponsesAdapterFromConfig(cfg)
	if err != nil {
		return fmt.Errorf("proxy providers: %w", err)
	}
	views := []proxyProviderView{proxyProviderViewFromStatus(adapter.Status(ctx))}
	if opts.JSON {
		return json.NewEncoder(os.Stdout).Encode(views)
	}
	for _, view := range views {
		fmt.Fprintf(os.Stdout, "%s (%s): ready=%t base_url=%s\n", view.Provider, view.DisplayName, view.Ready, view.BaseURL)
	}
	return nil
}

func proxyStart(ctx context.Context, args []string) error {
	opts, err := parseProxyOptions(args)
	if err != nil {
		return fmt.Errorf("proxy start: %w", err)
	}
	cfg, err := loadProxyCommandConfig()
	if err != nil {
		return fmt.Errorf("proxy start: %w", err)
	}
	adapter, err := providerproxy.OpenAIResponsesAdapterFromConfig(cfg)
	if err != nil {
		return fmt.Errorf("proxy start: %w", err)
	}
	status := adapter.Status(ctx)
	if !status.Ready {
		return fmt.Errorf("proxy start: provider %s is not ready: %s", status.Provider, status.AuthFailure)
	}
	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	server := &http.Server{
		Addr:    addr,
		Handler: providerproxy.NewHandler(adapter, providerproxy.ServerOptions{}),
	}
	if warning := proxyLANExposureWarning(opts.Host); warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}
	fmt.Fprintf(os.Stdout, "Elnath proxy listening on http://%s/v1\n", addr)
	if ctx != nil {
		go func() {
			<-ctx.Done()
			_ = server.Close()
		}()
	}
	err = server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

type proxyOptions struct {
	JSON bool
	Host string
	Port int
}

func parseProxyOptions(args []string) (proxyOptions, error) {
	opts := proxyOptions{Host: providerproxy.DefaultHost, Port: providerproxy.DefaultPort}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			opts.JSON = true
		case "--host":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--host requires a value")
			}
			i++
			opts.Host = args[i]
		case "--port":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--port requires a value")
			}
			i++
			port, err := strconv.Atoi(args[i])
			if err != nil || port <= 0 || port > 65535 {
				return opts, fmt.Errorf("--port must be 1-65535")
			}
			opts.Port = port
		case "--provider":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--provider requires a value")
			}
			i++
			if config.NormalizeProviderName(args[i]) != "openai-responses" {
				return opts, fmt.Errorf("only openai-responses proxy provider is supported in this milestone")
			}
		case "help", "-h", "--help":
			return opts, proxyUsage()
		default:
			return opts, fmt.Errorf("unknown flag %q", arg)
		}
	}
	return opts, nil
}

func proxyStatusViewForAdapter(ctx context.Context, adapter *providerproxy.OpenAIResponsesAdapter, opts proxyOptions, state string) proxyStatusView {
	providerStatus := proxyProviderViewFromStatus(adapter.Status(ctx))
	return proxyStatusView{
		State:              state,
		Host:               opts.Host,
		Port:               opts.Port,
		ListenURL:          fmt.Sprintf("http://%s/v1", net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))),
		Provider:           providerStatus.Provider,
		DisplayName:        providerStatus.DisplayName,
		Ready:              providerStatus.Ready,
		Authenticated:      providerStatus.Authenticated,
		AllowedPaths:       providerStatus.AllowedPaths,
		LANExposureWarning: proxyLANExposureWarning(opts.Host),
		ProviderStatus:     providerStatus,
	}
}

func proxyProviderViewFromStatus(status providerproxy.ProviderStatus) proxyProviderView {
	return proxyProviderView{
		Provider:         status.Provider,
		DisplayName:      status.DisplayName,
		Ready:            status.Ready,
		Authenticated:    status.Authenticated,
		APIKeyConfigured: status.APIKeyConfigured,
		BaseURL:          status.BaseURL,
		AllowedPaths:     status.AllowedPaths,
		Refreshable:      status.Refreshable,
		AuthFailure:      status.AuthFailure,
	}
}

func proxyLANExposureWarning(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || host == providerproxy.DefaultHost || host == "localhost" || host == "::1" {
		return ""
	}
	return "Warning: proxy host is not loopback. LAN exposure can leak provider access to other machines."
}

func loadProxyCommandConfig() (*config.Config, error) {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	applyGlobalFlagOverrides(cfg, os.Args)
	return cfg, nil
}
