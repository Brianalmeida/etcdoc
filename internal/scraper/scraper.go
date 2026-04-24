package scraper

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/brian/etcd-reliability-tool/internal/config"
)

type Scraper struct {
	client *http.Client
	url    string
}

func New(cfg *config.Config) (*Scraper, error) {
	var transport *http.Transport

	if strings.HasPrefix(cfg.Etcd.MetricsURL, "https://") {
		slog.Debug("Initializing HTTPS scraper", "url", cfg.Etcd.MetricsURL, "cert", cfg.Etcd.CertFile)
		
		// Load client cert
		cert, err := tls.LoadX509KeyPair(cfg.Etcd.CertFile, cfg.Etcd.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load etcd client cert [%s, %s]: %w", cfg.Etcd.CertFile, cfg.Etcd.KeyFile, err)
		}

		// Load CA cert
		caCert, err := os.ReadFile(cfg.Etcd.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read etcd CA cert [%s]: %w", cfg.Etcd.CAFile, err)
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      caCertPool,
		}
		transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	} else {
		slog.Debug("Initializing HTTP scraper", "url", cfg.Etcd.MetricsURL)
		transport = &http.Transport{}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	return &Scraper{
		client: client,
		url:    cfg.Etcd.MetricsURL,
	}, nil
}

func (s *Scraper) Scrape() (string, error) {
	slog.Debug("Scraping etcd metrics", "url", s.url)
	
	resp, err := s.client.Get(s.url)
	if err != nil {
		return "", fmt.Errorf("failed to GET metrics from %s: %w", s.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("received unexpected status code from %s: %d", s.url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body from %s: %w", s.url, err)
	}

	return string(body), nil
}
