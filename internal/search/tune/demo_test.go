package tune

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/km269/wukong/internal/search"
)

// TestAutoTune_Demo runs the full AutoTuneService workflow with a
// mock searcher and prints every stage's output so you can see the
// actual results. Run with:
//
//	go test -v -run TestAutoTune_Demo ./internal/search/tune/...
func TestAutoTune_Demo(t *testing.T) {
	ctx := context.Background()

	// --- 1. Build a mixed query set (keyword + natural language) ---
	cases := []search.EvalCase{
		{Query: "cortex config", QueryType: "keyword",
			RelevantIDs: []string{"doc_a", "doc_b"}},
		{Query: "git push", QueryType: "keyword",
			RelevantIDs: []string{"doc_a"}},
		{Query: "rpc timeout", QueryType: "keyword",
			RelevantIDs: []string{"doc_b"}},
		{Query: "how do I configure the search tuning system for hybrid recall",
			QueryType:   "natural_language",
			RelevantIDs: []string{"doc_a", "doc_c"}},
		{Query: "what is the difference between lexical and vector search",
			QueryType:   "natural_language",
			RelevantIDs: []string{"doc_c"}},
		{Query: "why are my images not downloading when cloning a site",
			QueryType:   "long_tail",
			RelevantIDs: []string{"doc_a"}},
	}

	// --- 2. Create the service (mock searcher, no LLM, no checkpoint file) ---
	svc, err := NewAutoTuneService(AutoTuneConfig{
		Searcher:     &mockSearcher{},
		DatasetLabel: "demo-dataset",
	})
	if err != nil {
		t.Fatalf("NewAutoTuneService: %v", err)
	}
	defer svc.Close()

	printBanner("1. PLAN (预算 + 策略提议，不调用搜索/LLM)")
	plan, err := svc.Plan(ctx, cases, PlanOptions{
		TopK:         10,
		SilverFilter: true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	printJSON("Validation", plan.Validation)
	printJSON("Distribution", plan.Distribution)
	fmt.Printf("  提议候选策略数: %d\n", len(plan.Strategies))
	for i, g := range plan.Strategies {
		fmt.Printf("    [%d] %s dense=%.2f text=%.2f topK=%d pool=%d\n",
			i, g.RecallMode, g.DenseWeight, g.TextWeight,
			g.MaxRetrievedNum, g.FTS5PoolSize)
	}
	printJSON("Budget", plan.Budget)
	fmt.Printf("  建议: %s\n", plan.Recommendation)

	printBanner("2. RUN (受控并发 + 标签缓存 + checkpoint)")
	run, err := svc.RunTuning(ctx, cases, plan.Strategies, RunOptions{
		RunID:       "demo-run",
		TopK:        10,
		Concurrency: 4,
		LabelSource: "prelabelled",
	})
	if err != nil {
		t.Fatalf("RunTuning: %v", err)
	}
	fmt.Printf("  RunID:    %s\n", run.RunID)
	fmt.Printf("  Status:   %s\n", run.Status)
	fmt.Printf("  Strategies: %d, Queries: %d\n",
		len(run.Request.Strategies), len(run.Request.Cases))
	fmt.Printf("  每策略指标 (NDCG@20 / NDCG@10 / MRR@10 / P@10 / ZeroR / LatencyMs):\n")
	for i, m := range run.Metrics {
		fmt.Printf("    [%d] %.4f / %.4f / %.4f / %.4f / %.2f / %.0fms\n",
			i, m.NDCG20, m.NDCG10, m.MRR10,
			m.Precision10, m.ZeroResultRate, m.AvgLatencyMs)
	}

	printBanner("3. REPORT (报告)")
	report, err := svc.Report("demo-run")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	printJSON("Report", report)

	printBanner("4. APPLY DRY-RUN (安全边界: 只展示 diff, 不变更)")
	current := search.DefaultGenome()
	applyPlan, err := svc.ApplyDryRun("demo-run", current)
	if err != nil {
		t.Fatalf("ApplyDryRun: %v", err)
	}
	fmt.Printf("  Confirmed: %v\n", applyPlan.Confirmed)
	fmt.Printf("  Current:  %s dense=%.2f text=%.2f\n",
		applyPlan.CurrentStrategy.RecallMode,
		applyPlan.CurrentStrategy.DenseWeight,
		applyPlan.CurrentStrategy.TextWeight)
	fmt.Printf("  Proposed: %s dense=%.2f text=%.2f\n",
		applyPlan.ProposedStrategy.RecallMode,
		applyPlan.ProposedStrategy.DenseWeight,
		applyPlan.ProposedStrategy.TextWeight)
	fmt.Printf("  Improvement: %.1f%%\n", applyPlan.Improvement*100)
	fmt.Printf("  Diff:\n")
	if len(applyPlan.Diff) == 0 {
		fmt.Printf("    (no changes — best strategy matches current)\n")
	}
	for _, d := range applyPlan.Diff {
		fmt.Printf("    %s: %v → %v\n", d.Field, d.OldValue, d.NewValue)
	}

	printBanner("5. APPLY CONFIRMED (只生成候选配置, 不切换默认入口)")
	confirmed, err := svc.Apply("demo-run", current, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	fmt.Printf("  Confirmed: %v\n", confirmed.Confirmed)
	fmt.Printf("  CandidateConfig (写入候选槽, 不影响活跃配置):\n")
	printJSON("  Candidate", confirmed.CandidateConfig)

	printBanner("6. RESUME (中断后续跑, 只重跑未完成部分)")
	resumed, err := svc.Resume(ctx, "demo-run")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	fmt.Printf("  Resumed RunID: %s, Status: %s\n",
		resumed.RunID, resumed.Status)
	fmt.Printf("  (全部已完成, 无需重跑)\n")

	printBanner("7. LABEL CACHE 隔离验证")
	inner := NewInMemoryLabelCache()
	c1 := &datasetAwareLabelCache{inner: inner, dataset: "ds1", judgeCfg: "llm-a"}
	c2 := &datasetAwareLabelCache{inner: inner, dataset: "ds2", judgeCfg: "llm-a"}
	c3 := &datasetAwareLabelCache{inner: inner, dataset: "ds1", judgeCfg: "llm-b"}
	c1.Set("q::doc", 3)
	_, ok1 := c1.Get("q::doc")
	_, ok2 := c2.Get("q::doc")
	_, ok3 := c3.Get("q::doc")
	fmt.Printf("  同 dataset+judge 命中: %v\n", ok1)
	fmt.Printf("  不同 dataset 命中:     %v (应为 false)\n", ok2)
	fmt.Printf("  不同 judge 命中:       %v (应为 false)\n", ok3)

	fmt.Println("\n════════════════════════════════════════")
	fmt.Println("  Demo 完成. 全流程: Plan→Run→Report→Apply→Resume")
	fmt.Println("════════════════════════════════════════")
}

func printBanner(title string) {
	fmt.Printf("\n════════════════════════════════════════\n")
	fmt.Printf("  %s\n", title)
	fmt.Printf("════════════════════════════════════════\n")
}

func printJSON(label string, v any) {
	data, _ := json.MarshalIndent(v, "  ", "  ")
	fmt.Printf("  %s:\n  %s\n", label, string(data))
}
