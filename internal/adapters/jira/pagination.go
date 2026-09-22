package jira

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

const maxPages = 200
const maxRecords = 10000

func (c *Client) allSprints(ctx context.Context, board string) ([]jiraSprint, error) {
	var out []jiraSprint
	seen := map[int]bool{}
	for start, pages := 0, 0; ; pages++ {
		if pages >= maxPages {
			return nil, kernel.Invalid("pagination", "Jira sprint page limit exceeded")
		}
		q := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(c.fields.MaxResults)}}
		var page struct {
			Values []jiraSprint `json:"values"`
			IsLast bool         `json:"isLast"`
		}
		if err := c.do(ctx, http.MethodGet, "/rest/agile/1.0/board/"+url.PathEscape(board)+"/sprint", q, nil, &page); err != nil {
			return nil, err
		}
		for _, s := range page.Values {
			if s.ID <= 0 || seen[s.ID] {
				return nil, kernel.Invalid("pagination", "Jira repeated or invalid sprint identifier")
			}
			seen[s.ID] = true
			out = append(out, s)
		}
		if len(out) > maxRecords {
			return nil, kernel.Invalid("pagination", "Jira sprint limit exceeded")
		}
		if page.IsLast || len(page.Values) == 0 {
			return out, nil
		}
		start += len(page.Values)
	}
}

func (c *Client) sprintIssues(ctx context.Context, sprint int) ([]jiraIssue, error) {
	var out []jiraIssue
	seen := map[string]bool{}
	for start, pages := 0, 0; ; pages++ {
		if pages >= maxPages {
			return nil, kernel.Invalid("pagination", "Jira issue page limit exceeded")
		}
		q := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(c.fields.MaxResults)}, "fields": {"summary,status,created,closedSprints,sprint"}}
		var page jiraSearch
		if err := c.do(ctx, http.MethodGet, "/rest/agile/1.0/sprint/"+strconv.Itoa(sprint)+"/issue", q, nil, &page); err != nil {
			return nil, err
		}
		for _, is := range page.Issues {
			if is.Key == "" || seen[is.Key] {
				return nil, kernel.Invalid("pagination", "Jira repeated or invalid issue identifier")
			}
			seen[is.Key] = true
			out = append(out, is)
		}
		if len(out) > maxRecords {
			return nil, kernel.Invalid("pagination", "Jira issue limit exceeded")
		}
		start += len(page.Issues)
		if start >= page.Total {
			return out, nil
		}
		if len(page.Issues) == 0 {
			return nil, kernel.Invalid("pagination", "Jira incomplete issue page")
		}
	}
}

type jiraWorklog struct {
	ID     string `json:"id"`
	Author struct {
		Key       string `json:"key"`
		AccountID string `json:"accountId"`
	} `json:"author"`
	Started string `json:"started"`
	Updated string `json:"updated"`
	Seconds int64  `json:"timeSpentSeconds"`
}

// Worklogs reads complete issue worklog lists, then filters by Started. This
// deliberately includes corrections to old records and supports snapshot replacement.
func (c *Client) Worklogs(ctx context.Context, issueKeys []string, since time.Time) ([]ports.Worklog, error) {
	if len(issueKeys) > maxRecords {
		return nil, kernel.Invalid("issue_keys", "too many issues")
	}
	for _, key := range issueKeys {
		if err := validKey(key); err != nil {
			return nil, err
		}
	}
	seenIssues := map[string]bool{}
	seenLogs := map[string]bool{}
	var out []ports.Worklog
	total := 0
	for _, key := range issueKeys {
		if seenIssues[key] {
			continue
		}
		seenIssues[key] = true
		for start, pages := 0, 0; ; pages++ {
			if pages >= maxPages {
				return nil, kernel.Invalid("pagination", "Jira worklog page limit exceeded")
			}
			q := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(c.fields.MaxResults)}}
			var page struct {
				Total    int           `json:"total"`
				Worklogs []jiraWorklog `json:"worklogs"`
			}
			if err := c.do(ctx, http.MethodGet, "/rest/api/2/issue/"+url.PathEscape(key)+"/worklog", q, nil, &page); err != nil {
				return nil, err
			}
			for _, w := range page.Worklogs {
				total++
				if total > maxRecords {
					return nil, kernel.Invalid("worklogs", "Jira worklog limit exceeded")
				}
				if w.ID == "" || len(w.ID) > MaxKey || seenLogs[key+":"+w.ID] {
					return nil, kernel.Invalid("worklog_id", "missing, too long or duplicated")
				}
				seenLogs[key+":"+w.ID] = true
				started, updated := timeOf(w.Started), timeOf(w.Updated)
				if started.IsZero() || updated.IsZero() {
					return nil, kernel.Invalid("worklog_time", "invalid timestamp")
				}
				if w.Seconds < 0 || w.Seconds > math.MaxInt64/int64(time.Second) {
					return nil, kernel.Invalid("worklog_duration", "negative or overflowing duration")
				}
				author := w.Author.Key
				if author == "" {
					author = w.Author.AccountID
				}
				if author == "" || len(author) > MaxKey {
					return nil, kernel.Invalid("worklog_author", "opaque author key is required")
				}
				if !started.Before(since) {
					out = append(out, ports.Worklog{ExternalID: w.ID, IssueKey: key, Author: author, Started: started, UpdatedAt: updated, Spent: time.Duration(w.Seconds) * time.Second})
				}
			}
			start += len(page.Worklogs)
			if start >= page.Total {
				break
			}
			if len(page.Worklogs) == 0 {
				return nil, kernel.Invalid("pagination", "Jira incomplete worklog page")
			}
		}
	}
	return out, nil
}
