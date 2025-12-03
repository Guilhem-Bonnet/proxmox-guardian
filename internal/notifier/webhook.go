package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/template"
	"time"
)

// Notifier sends notifications to various channels
type Notifier struct {
	webhooks []WebhookConfig
	client   *http.Client
}

// WebhookConfig defines a webhook notification target
type WebhookConfig struct {
	URL      string
	URLEnv   string
	Events   []string
	Template string
}

// NewNotifier creates a new notifier
func NewNotifier(webhooks []WebhookConfig) *Notifier {
	return &Notifier{
		webhooks: webhooks,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Notify sends a notification for the given event
func (n *Notifier) Notify(event string, data map[string]interface{}) error {
	var lastErr error

	for _, webhook := range n.webhooks {
		if !n.shouldNotify(webhook, event) {
			continue
		}

		if err := n.sendWebhook(webhook, event, data); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

func (n *Notifier) shouldNotify(webhook WebhookConfig, event string) bool {
	if len(webhook.Events) == 0 {
		return true // No filter = all events
	}

	for _, e := range webhook.Events {
		if e == event || e == "*" {
			return true
		}
	}

	return false
}

func (n *Notifier) sendWebhook(webhook WebhookConfig, event string, data map[string]interface{}) error {
	url := webhook.URL
	if webhook.URLEnv != "" {
		url = os.Getenv(webhook.URLEnv)
	}

	if url == "" {
		return fmt.Errorf("webhook URL not configured")
	}

	// Build payload
	payload := n.buildPayload(webhook, event, data)

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	resp, err := n.client.Post(url, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("sending webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

func (n *Notifier) buildPayload(webhook WebhookConfig, event string, data map[string]interface{}) map[string]interface{} {
	// Default Discord-style payload
	if webhook.Template == "" {
		return n.buildDiscordPayload(event, data)
	}

	// Custom template
	tmpl, err := template.New("webhook").Parse(webhook.Template)
	if err != nil {
		return map[string]interface{}{
			"event": event,
			"data":  data,
			"error": "template parse error",
		}
	}

	var buf bytes.Buffer
	templateData := map[string]interface{}{
		"event":     event,
		"data":      data,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	if err := tmpl.Execute(&buf, templateData); err != nil {
		return map[string]interface{}{
			"event": event,
			"data":  data,
			"error": "template exec error",
		}
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		return map[string]interface{}{
			"content": buf.String(),
		}
	}

	return result
}

func (n *Notifier) buildDiscordPayload(event string, data map[string]interface{}) map[string]interface{} {
	// Map events to colors and emojis
	eventConfig := map[string]struct {
		emoji string
		color int
		title string
	}{
		"power_lost":        {"⚡", 0xFF0000, "Power Lost"},
		"power_restored":    {"✅", 0x00FF00, "Power Restored"},
		"shutdown_start":    {"🚀", 0xFFA500, "Shutdown Starting"},
		"shutdown_complete": {"🛑", 0x00FF00, "Shutdown Complete"},
		"phase_start":       {"📋", 0x3498DB, "Phase Started"},
		"phase_complete":    {"✓", 0x2ECC71, "Phase Completed"},
		"recovery_start":    {"🔄", 0x9B59B6, "Recovery Starting"},
		"recovery_complete": {"✅", 0x00FF00, "Recovery Complete"},
		"error":             {"❌", 0xFF0000, "Error"},
	}

	config, ok := eventConfig[event]
	if !ok {
		config = eventConfig["error"]
		config.title = event
	}

	// Build description from data
	description := ""
	for k, v := range data {
		description += fmt.Sprintf("**%s**: %v\n", k, v)
	}

	return map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       fmt.Sprintf("%s %s", config.emoji, config.title),
				"description": description,
				"color":       config.color,
				"timestamp":   time.Now().Format(time.RFC3339),
				"footer": map[string]interface{}{
					"text": "Proxmox Guardian",
				},
			},
		},
	}
}
