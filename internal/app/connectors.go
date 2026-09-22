package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onixus/metis/internal/adapters/bitrix"
	"github.com/onixus/metis/internal/adapters/confluence"
	"github.com/onixus/metis/internal/adapters/crmfile"
	"github.com/onixus/metis/internal/adapters/jira"
	"github.com/onixus/metis/internal/kernel"
)

// configureConnectors is the sole provider-selection boundary. Domain modules and
// scheduler consume ports; adding a provider does not introduce vendor code there.
func (a *App) configureConnectors() error {
	client := &http.Client{Timeout: 15 * time.Second}
	cfg := &a.Cfg
	if cfg.DeliveryProvider == "" {
		cfg.DeliveryProvider = "none"
		if cfg.JiraBaseURL != "" {
			cfg.DeliveryProvider = "jira"
		}
	}
	switch cfg.DeliveryProvider {
	case "none":
	case "jira":
		if cfg.JiraBaseURL == "" || cfg.JiraToken == "" {
			return kernel.Invalid("jira_config", "base URL and token are required")
		}
		fields := jira.DefaultFieldConfig()
		if err := connectorJSON(cfg.JiraFieldsFile, &fields); err != nil {
			return err
		}
		tracker, err := jira.New(cfg.JiraBaseURL, jira.NewStaticToken(cfg.JiraToken), fields, client)
		if err != nil {
			return err
		}
		a.Tracker, a.webhookParser = tracker, tracker
	default:
		return kernel.Invalid("delivery_provider", "unknown provider; supported: none, jira")
	}
	if cfg.KnowledgeProvider == "" {
		cfg.KnowledgeProvider = "none"
		if cfg.ConfluenceBaseURL != "" {
			cfg.KnowledgeProvider = "confluence"
		}
	}
	switch cfg.KnowledgeProvider {
	case "none":
	case "confluence":
		if cfg.ConfluenceBaseURL == "" || cfg.ConfluenceToken == "" {
			return kernel.Invalid("confluence_config", "base URL and token are required")
		}
		kb, err := confluence.New(cfg.ConfluenceBaseURL, confluence.NewStaticToken(cfg.ConfluenceToken), client)
		if err != nil {
			return err
		}
		a.Knowledge = kb
		if cfg.ConfluenceSpace == "" {
			cfg.ConfluenceSpace = "METIS"
		}
	default:
		return kernel.Invalid("knowledge_provider", "unknown provider; supported: none, confluence")
	}
	if cfg.CRMProvider == "" {
		cfg.CRMProvider = "none"
		if cfg.CRMDir != "" && cfg.BitrixBaseURL != "" {
			return kernel.Invalid("crm_provider", "choose csv or bitrix24 explicitly when both sources are configured")
		}
		if cfg.CRMDir != "" {
			cfg.CRMProvider = "csv"
		}
		if cfg.BitrixBaseURL != "" {
			cfg.CRMProvider = "bitrix24"
		}
	}
	switch cfg.CRMProvider {
	case "none":
	case "csv":
		if cfg.CRMDir == "" {
			return kernel.Invalid("crm_dir", "required for csv provider")
		}
		a.CRM = crmfile.New(cfg.CRMDir)
	case "bitrix24":
		if cfg.BitrixBaseURL == "" || cfg.BitrixToken == "" || cfg.BitrixFieldsFile == "" {
			return kernel.Invalid("bitrix_config", "base URL, token and fields file are required")
		}
		fields := bitrix.DefaultFields()
		if err := connectorJSON(cfg.BitrixFieldsFile, &fields); err != nil {
			return err
		}
		crm, err := bitrix.New(cfg.BitrixBaseURL, bitrix.NewStaticToken(cfg.BitrixToken), fields, client)
		if err != nil {
			return err
		}
		a.CRM = crm
	default:
		return kernel.Invalid("crm_provider", "unknown provider; supported: none, csv, bitrix24")
	}
	return nil
}

func connectorJSON(path string, out any) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path) // #nosec G304 -- administrator-selected configuration file, read-only
	if err != nil {
		return fmt.Errorf("%w: connector fields file cannot be opened", kernel.ErrValidation)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return kernel.Invalid("connector_fields", "file exceeds 64 KiB or cannot be read")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return kernel.Invalid("connector_fields", "invalid JSON mapping")
	}
	if d.Decode(new(any)) != io.EOF {
		return kernel.Invalid("connector_fields", "expected one JSON object")
	}
	return nil
}
