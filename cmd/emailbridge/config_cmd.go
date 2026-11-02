package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/radiobros/emailbridge/internal/configcode"
)

// configCmd implements `emailbridge config export|plan|apply`.
// It talks only to the API (Bearer + EB-Organization). Values of secrets are never printed.
func configCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: emailbridge config export|plan|apply -f FILE --org ORG --url URL --token TOKEN [--prune] [--output json]")
		return 2
	}
	sub, args := args[0], args[1:]
	var (
		file, org, baseURL, token, output string
		prune                             bool
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--file":
			v, next, ok := takeFlagArg(args, i)
			if !ok {
				_, _ = fmt.Fprintln(stderr, "config: -f requires a path")
				return 2
			}
			file, i = v, next
		case "--org":
			v, next, ok := takeFlagArg(args, i)
			if !ok {
				_, _ = fmt.Fprintln(stderr, "config: --org requires a value")
				return 2
			}
			org, i = v, next
		case "--url":
			v, next, ok := takeFlagArg(args, i)
			if !ok {
				_, _ = fmt.Fprintln(stderr, "config: --url requires a value")
				return 2
			}
			baseURL, i = v, next
		case "--token":
			v, next, ok := takeFlagArg(args, i)
			if !ok {
				_, _ = fmt.Fprintln(stderr, "config: --token requires a value")
				return 2
			}
			token, i = v, next
		case "--output":
			v, next, ok := takeFlagArg(args, i)
			if !ok {
				_, _ = fmt.Fprintln(stderr, "config: --output requires a value")
				return 2
			}
			output, i = v, next
		case "--prune":
			prune = true
		default:
			_, _ = fmt.Fprintf(stderr, "config: unknown flag %q\n", args[i])
			return 2
		}
	}
	if token == "" {
		token = os.Getenv("EB_TOKEN")
	}
	if baseURL == "" || org == "" || token == "" {
		_, _ = fmt.Fprintln(stderr, "config: --url, --org and --token (or EB_TOKEN) are required")
		return 2
	}
	baseURL = strings.TrimRight(baseURL, "/")
	client := &http.Client{Timeout: 60 * time.Second}

	switch sub {
	case "export":
		return configExport(client, baseURL, org, token, output, stdout, stderr)
	case "plan":
		if file == "" {
			_, _ = fmt.Fprintln(stderr, "config plan: -f FILE is required")
			return 2
		}
		return configPlan(client, baseURL, org, token, file, prune, output, stdout, stderr)
	case "apply":
		if file == "" {
			_, _ = fmt.Fprintln(stderr, "config apply: -f FILE is required")
			return 2
		}
		return configApply(client, baseURL, org, token, file, prune, output, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "config: unknown subcommand %q\n", sub)
		return 2
	}
}

// takeFlagArg returns args[i+1] after a flag at i. Safe for gosec G602.
func takeFlagArg(args []string, i int) (string, int, bool) {
	next := i + 1
	if next >= len(args) {
		return "", i, false
	}
	return args[next], next, true
}

func configExport(client *http.Client, base, org, token, output string, stdout, stderr io.Writer) int {
	req, err := http.NewRequest(http.MethodGet, base+"/v1/config/export", nil) //nolint:gosec // G704: the operator passed this server URL with --url
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	setConfigHeaders(req, org, token)
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req) //nolint:gosec // G704: the CLI calls the server URL its operator passed with --url
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "config export: %s\n%s\n", res.Status, redactBody(body))
		return 1
	}
	if output == "json" {
		var buf bytes.Buffer
		if err := json.Indent(&buf, body, "", "  "); err != nil {
			_, _ = fmt.Fprintln(stdout, string(body))
			return 0
		}
		_, _ = fmt.Fprintln(stdout, buf.String())
		return 0
	}
	var doc configcode.Document
	if err := json.Unmarshal(body, &doc); err != nil {
		_, _ = fmt.Fprintln(stderr, "config export: invalid response")
		return 1
	}
	yb, err := yaml.Marshal(&doc)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprint(stdout, string(yb))
	return 0
}

func configPlan(client *http.Client, base, org, token, file string, prune bool, output string, stdout, stderr io.Writer) int {
	doc, err := loadConfigFile(file)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	payload, _ := json.Marshal(configcode.PlanRequest{Document: *doc, Prune: prune})
	req, err := http.NewRequest(http.MethodPost, base+"/v1/config/plan", bytes.NewReader(payload)) //nolint:gosec // G704: the operator passed this server URL with --url
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	setConfigHeaders(req, org, token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req) //nolint:gosec // G704: the CLI calls the server URL its operator passed with --url
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "config plan: %s\n%s\n", res.Status, redactBody(body))
		return 1
	}
	return printPlan(body, output, stdout, stderr)
}

func configApply(client *http.Client, base, org, token, file string, prune bool, output string, stdout, stderr io.Writer) int {
	doc, err := loadConfigFile(file)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	planPayload, _ := json.Marshal(configcode.PlanRequest{Document: *doc, Prune: prune})
	planReq, err := http.NewRequest(http.MethodPost, base+"/v1/config/plan", bytes.NewReader(planPayload)) //nolint:gosec // G704: the operator passed this server URL with --url
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	setConfigHeaders(planReq, org, token)
	planReq.Header.Set("Content-Type", "application/json")
	planRes, err := client.Do(planReq) //nolint:gosec // G704: the CLI calls the server URL its operator passed with --url
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	planBody, err := io.ReadAll(planRes.Body)
	_ = planRes.Body.Close()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if planRes.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "config apply (plan): %s\n%s\n", planRes.Status, redactBody(planBody))
		return 1
	}
	var plan configcode.Plan
	if err := json.Unmarshal(planBody, &plan); err != nil {
		_, _ = fmt.Fprintln(stderr, "config apply: invalid plan response")
		return 1
	}
	applyPayload, _ := json.Marshal(configcode.ApplyRequest{
		Document: *doc, Prune: prune, PlanHash: plan.PlanHash,
	})
	applyReq, err := http.NewRequest(http.MethodPost, base+"/v1/config/apply", bytes.NewReader(applyPayload)) //nolint:gosec // G704: the operator passed this server URL with --url
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	setConfigHeaders(applyReq, org, token)
	applyReq.Header.Set("Content-Type", "application/json")
	applyRes, err := client.Do(applyReq) //nolint:gosec // G704: the CLI calls the server URL its operator passed with --url
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = applyRes.Body.Close() }()
	body, _ := io.ReadAll(applyRes.Body)
	if applyRes.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "config apply: %s\n%s\n", applyRes.Status, redactBody(body))
		return 1
	}
	if output == "json" {
		_, _ = fmt.Fprintln(stdout, string(body))
		return 0
	}
	var wrap struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	if json.Unmarshal(body, &wrap) == nil && wrap.Job.ID != "" {
		_, _ = fmt.Fprintf(stdout, "job %s accepted (plan %s, %d actions)\n", wrap.Job.ID, plan.PlanHash, len(plan.Actions))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, string(body))
	return 0
}

func loadConfigFile(path string) (*configcode.Document, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-chosen config file
	if err != nil {
		return nil, err
	}
	var doc configcode.Document
	if err := yaml.Unmarshal(b, &doc); err != nil {
		if err2 := json.Unmarshal(b, &doc); err2 != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if doc.APIVersion == "" {
		doc.APIVersion = configcode.APIVersion
	}
	if doc.Kind == "" {
		doc.Kind = configcode.KindOrg
	}
	return &doc, nil
}

func setConfigHeaders(req *http.Request, org, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("EB-Organization", org)
	req.Header.Set("X-Requested-With", "EmailBridge")
}

func printPlan(body []byte, output string, stdout, stderr io.Writer) int {
	if output == "json" {
		var buf bytes.Buffer
		if err := json.Indent(&buf, body, "", "  "); err != nil {
			_, _ = fmt.Fprintln(stdout, string(body))
			return 0
		}
		_, _ = fmt.Fprintln(stdout, buf.String())
		return 0
	}
	var plan configcode.Plan
	if err := json.Unmarshal(body, &plan); err != nil {
		_, _ = fmt.Fprintln(stderr, "config plan: invalid response")
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "plan %s (%d actions, prune=%v)\n", plan.PlanHash, len(plan.Actions), plan.Prune)
	for _, a := range plan.Actions {
		line := fmt.Sprintf("  %s %s %s", a.Op, a.Kind, a.Name)
		if a.ID != "" {
			line += " (" + a.ID + ")"
		}
		_, _ = fmt.Fprintln(stdout, line)
		for k, v := range a.Changes {
			_, _ = fmt.Fprintf(stdout, "    %s: %v\n", k, v)
		}
	}
	return 0
}

// redactBody strips likely secret-looking JSON string values before printing errors.
func redactBody(b []byte) string {
	s := string(b)
	// Never echo Authorization or raw secret fields from problem extensions.
	if strings.Contains(strings.ToLower(s), "whsec_") || strings.Contains(strings.ToLower(s), "ebk_") {
		return "(response redacted)"
	}
	if len(s) > 2000 {
		return s[:2000] + "…"
	}
	return s
}
