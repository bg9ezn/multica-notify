package message

import (
	"bytes"
	"fmt"
	"text/template"
	"time"

	"github.com/bg9ezn/multica-notify/internal/config"
	"github.com/bg9ezn/multica-notify/internal/event"
)

// template set names as addressed by config templates overrides.
const (
	SetIssueStatus = "issue_status"
	SetTask        = "task"
	SetHeartbeat   = "heartbeat"
)

var builtinSets = map[string]struct{ Title, Body string }{
	SetIssueStatus: {
		Title: `[Multica] {{ref .}} is now {{.Issue.Status}}`,
		Body:  "{{.Issue.Title}}\nstatus: {{.Issue.Status}}",
	},
	SetTask: {
		Title: `[Multica] task {{ref .}}: {{taskState .}}`,
		Body:  `{{taskBody .}}`,
	},
	SetHeartbeat: {
		Title: `Multica notify is alive`,
		Body:  `heartbeat delivery {{.DeliveryID}} received at {{now}}`,
	},
}

// Renderer executes the built-in templates with any config overrides applied.
// A broken override fails at startup, not mid-incident.
type Renderer struct {
	sets map[string]set
}

type set struct {
	title *template.Template
	body  *template.Template
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"ref": func(e *event.Event) string { return e.Ref() },
		"now": func() string { return time.Now().Format(time.RFC3339) },
		"taskState": func(e *event.Event) string {
			if e.Task == nil {
				return e.EventType
			}
			if e.EventType == event.EventTaskFailed && e.Task.RetryPending {
				return "failed, retry pending"
			}
			return e.Task.Status
		},
		"taskBody": func(e *event.Event) string {
			if e.Task == nil {
				return e.EventType
			}
			if e.EventType == event.EventTaskFailed {
				if e.Task.RetryPending {
					return fmt.Sprintf("attempt failed (%s), Multica will retry", e.Task.FailureReason)
				}
				if e.Task.FailureReason != "" {
					return "failure reason: " + e.Task.FailureReason
				}
				return "the agent run failed."
			}
			return "the agent run completed."
		},
	}
}

func NewRenderer(overrides map[string]config.TemplateConfig) (*Renderer, error) {
	r := &Renderer{sets: make(map[string]set, len(builtinSets))}
	sources := make(map[string]struct{ Title, Body string }, len(builtinSets)+len(overrides))
	for name, builtin := range builtinSets {
		sources[name] = builtin
	}
	for name, override := range overrides {
		if _, known := builtinSets[name]; !known {
			return nil, fmt.Errorf("templates: unknown template set %q (known: issue_status, task, heartbeat)", name)
		}
		sources[name] = struct{ Title, Body string }{override.Title, override.Body}
	}
	for name, src := range sources {
		title, err := template.New(name + ".title").Funcs(funcMap()).Parse(src.Title)
		if err != nil {
			return nil, fmt.Errorf("templates: %s.title: %w", name, err)
		}
		body, err := template.New(name + ".body").Funcs(funcMap()).Parse(src.Body)
		if err != nil {
			return nil, fmt.Errorf("templates: %s.body: %w", name, err)
		}
		r.sets[name] = set{title: title, body: body}
	}
	return r, nil
}

// Render renders the event through the template set for its kind.
func (r *Renderer) Render(e *event.Event) (Message, error) {
	setName := setFor(e)
	s, ok := r.sets[setName]
	if !ok {
		return Message{}, fmt.Errorf("message: no template set for %q", setName)
	}
	var title, body bytes.Buffer
	if err := s.title.Execute(&title, e); err != nil {
		return Message{}, fmt.Errorf("message: render title: %w", err)
	}
	if err := s.body.Execute(&body, e); err != nil {
		return Message{}, fmt.Errorf("message: render body: %w", err)
	}
	return Message{
		Title: title.String(),
		Body:  body.String(),
		Meta: map[string]string{
			"hook_key":    e.HookKey,
			"event_type":  e.EventType,
			"delivery_id": e.DeliveryID,
			"trigger":     e.Trigger,
		},
	}, nil
}

func setFor(e *event.Event) string {
	switch {
	case e.Trigger == event.TriggerSchedule:
		return SetHeartbeat
	case e.EventType == event.EventIssueStatusChanged:
		return SetIssueStatus
	default:
		return SetTask
	}
}
