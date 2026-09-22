package jira

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// ErrNotEpicEvent — событие webhook не относится к эпику или не отслеживается; вызывающий игнорирует его.
var ErrNotEpicEvent = ports.ErrIgnoredWebhook

// MaxWebhookBody — предел размера тела webhook.
const MaxWebhookBody = 1 << 20

type webhookPayload struct {
	Timestamp    int64  `json:"timestamp"`
	WebhookEvent string `json:"webhookEvent"`
	Issue        struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Fields struct {
			IssueType jiraNamed  `json:"issuetype"`
			DueDate   string     `json:"duedate"`
			Status    jiraStatus `json:"status"`
		} `json:"fields"`
	} `json:"issue"`
	Changelog struct {
		ID    string `json:"id"`
		Items []struct {
			Field    string `json:"field"`
			ToString string `json:"toString"`
			To       string `json:"to"`
		} `json:"items"`
	} `json:"changelog"`
}

// ParseWebhook разбирает тело webhook Jira (jira:issue_updated, jira:issue_created, jira:issue_deleted)
// в ports.WebhookEvent. Для событий не по эпику возвращает ErrNotEpicEvent.
// ExternalID = "<issue id>:<changelog id>" (для created/deleted — "<issue id>:<event>:<timestamp>"),
// что делает повторную доставку того же события распознаваемой (ТЗ 4.2).
func (c *Client) ParseWebhook(body []byte) (ports.WebhookEvent, error) {
	if len(body) > MaxWebhookBody {
		return ports.WebhookEvent{}, kernel.Invalid("body", "тело webhook слишком велико")
	}
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.WebhookEvent{}, fmt.Errorf("%w: webhook jira: %w", kernel.ErrValidation, err)
	}
	if p.Issue.Key == "" || p.Issue.ID == "" {
		return ports.WebhookEvent{}, kernel.Invalid("issue", "нет ключа задачи")
	}
	if p.Issue.Fields.IssueType.Name != c.fields.EpicIssueType {
		return ports.WebhookEvent{}, ErrNotEpicEvent
	}
	if err := validKey(p.Issue.Key); err != nil {
		return ports.WebhookEvent{}, err
	}
	ev := ports.WebhookEvent{
		EpicKey:    p.Issue.Key,
		OccurredAt: time.UnixMilli(p.Timestamp).UTC(),
		NewDueDate: dateOf(p.Issue.Fields.DueDate),
		NewStatus:  Sanitize(p.Issue.Fields.Status.Name, MaxKey),
	}
	switch p.WebhookEvent {
	case "jira:issue_updated":
		ev.Type = ports.WebhookEpicUpdated
		if p.Changelog.ID == "" {
			return ports.WebhookEvent{}, kernel.Invalid("changelog", "нет идентификатора изменения")
		}
		ev.ExternalID = p.Issue.ID + ":" + p.Changelog.ID
		for _, it := range p.Changelog.Items {
			switch it.Field {
			case "duedate":
				ev.ChangedFields = append(ev.ChangedFields, "due_date")
				ev.NewDueDate = dateOf(it.ToString)
			case "Fix Version", "fixVersions":
				ev.ChangedFields = append(ev.ChangedFields, "fix_versions")
			case "status":
				ev.ChangedFields = append(ev.ChangedFields, "status")
				ev.NewStatus = Sanitize(it.ToString, MaxKey)
			case "summary":
				ev.ChangedFields = append(ev.ChangedFields, "summary")
			}
		}
		if len(ev.ChangedFields) == 0 {
			return ports.WebhookEvent{}, ErrNotEpicEvent
		}
	case "jira:issue_created":
		ev.Type = ports.WebhookEpicCreated
		ev.ExternalID = p.Issue.ID + ":created:" + strconv.FormatInt(p.Timestamp, 10)
	case "jira:issue_deleted":
		ev.Type = ports.WebhookEpicDeleted
		ev.ExternalID = p.Issue.ID + ":deleted:" + strconv.FormatInt(p.Timestamp, 10)
	default:
		return ports.WebhookEvent{}, ErrNotEpicEvent
	}
	return ev, nil
}
