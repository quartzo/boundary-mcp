// Command judge-eval runs the labeled proposal benchmark against the JEV judge.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"boundary-mcp/internal/judge"
)

func main() {
	casesDir := flag.String("cases", "internal/judge/testdata/cases", "directory of labeled case JSON files")
	apiKey := flag.String("api-key", "", "OpenRouter API key (defaults to OPENROUTER_API_KEY or opencode auth)")
	baseURL := flag.String("base-url", envOr("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"), "System One base URL")
	model := flag.String("model", envOr("JUDGE_MODEL", "typesafe/jev-1.14"), "JEV model ID")
	reviewAt := flag.Float64("review-at", 0.30, "review threshold")
	rejectAt := flag.Float64("reject-at", 0.70, "reject threshold")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall timeout")
	flag.Parse()

	key := judge.ResolveAPIKey(*apiKey)
	if strings.TrimSpace(key) == "" {
		fmt.Fprintln(os.Stderr, "judge-eval: set OPENROUTER_API_KEY, -api-key, or opencode auth")
		os.Exit(2)
	}

	cases, err := judge.LoadCases(*casesDir)
	if err != nil {
		fail(err)
	}
	if len(cases) == 0 {
		fail(fmt.Errorf("no cases found in %s", *casesDir))
	}

	client, err := judge.NewOpenRouter(judge.OpenRouterOptions{
		BaseURL: *baseURL,
		APIKey:  key,
		Model:   *model,
	})
	if err != nil {
		fail(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	report := judge.EvaluateCases(ctx, judge.NewCached(client, client.Model()), cases, judge.Policy{
		ReviewAt: *reviewAt,
		RejectAt: *rejectAt,
	})

	printReport(report)
	if report.Errors > 0 {
		os.Exit(1)
	}
}

func printReport(report *judge.Report) {
	fmt.Printf("%-30s %-7s %-7s %-28s %5s %8s %8s\n", "CASE", "WANT", "GOT", "MAX RULE", "P", "LATENCY", "COST")
	for _, result := range report.Results {
		if result.Err != nil {
			fmt.Printf("%-30s %-7s %-7s %v\n", result.Case.Name, result.Case.Want, "ERROR", result.Err)
			continue
		}
		fmt.Printf("%-30s %-7s %-7s %-28s %5.2f %8s %8.5f\n",
			result.Case.Name,
			result.Case.Want,
			result.Verdict.Decision,
			result.Verdict.MaxRule,
			result.Verdict.MaxProbability,
			result.Assessment.Latency.Round(time.Millisecond),
			result.Assessment.CostUSD,
		)
	}

	fmt.Printf("\ntotal=%d correct=%d accuracy=%.2f errors=%d false_allow=%d false_escalate=%d p95=%s cost=%.5f\n",
		report.Total,
		report.Correct,
		report.Accuracy(),
		report.Errors,
		report.FalseAllow,
		report.FalseEscalate,
		report.P95Latency().Round(time.Millisecond),
		report.CostUSD,
	)

	fmt.Println("\nconfusion (want -> got):")
	for want, row := range report.Confusion() {
		for got, count := range row {
			fmt.Printf("  %-7s -> %-7s %d\n", want, got, count)
		}
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "judge-eval: %v\n", err)
	os.Exit(2)
}
