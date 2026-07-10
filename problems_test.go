package tracecheck

import "testing"

func TestProblemClassificationStableKeys(t *testing.T) {
	cfg := cfgT(t)
	tests := []struct {
		name        string
		message     string
		key         string
		code        string
		baselinable bool
	}{
		{
			name:        "base coverage",
			message:     "REQ-CORE-001 (MUST, §1): no tagged test or waiver",
			key:         "coverage-required:REQ-CORE-001",
			code:        "coverage-required",
			baselinable: true,
		},
		{
			name:        "coverage class",
			message:     "REQ-CORE-001 (§1): policy requires integration coverage",
			key:         "coverage-class-required:REQ-CORE-001:integration",
			code:        "coverage-class-required",
			baselinable: true,
		},
		{
			name:        "waiver reason",
			message:     `REQ-CORE-001 (§1): waiver reason "not-implemented" does not satisfy strict coverage`,
			key:         "waiver-reason-not-accepted:REQ-CORE-001:not-implemented",
			code:        "waiver-reason-not-accepted",
			baselinable: true,
		},
		{
			name:        "covered-by proxy",
			message:     "REQ-CORE-001 (§1): covered-by target REQ-CORE-002 has no tagged test for strict coverage",
			key:         "waiver-proxy-not-covered:REQ-CORE-001:REQ-CORE-002",
			code:        "waiver-proxy-not-covered",
			baselinable: true,
		},
		{
			name:    "integrity",
			message: "requirements.md: duplicate requirement ID REQ-CORE-001",
			code:    "integrity",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.classifyProblem(tt.message)
			if got.Code != tt.code || got.Baselinable != tt.baselinable {
				t.Fatalf("classifyProblem() = %+v", got)
			}
			if tt.key != "" && got.Key != tt.key {
				t.Fatalf("key = %q, want %q", got.Key, tt.key)
			}
			if got.Key == "" {
				t.Fatal("empty problem key")
			}
		})
	}
}

func TestProblemClassificationNeverBaselinesEmbeddedTriggerText(t *testing.T) {
	cfg := cfgT(t)
	messages := []string{
		`REQ-CORE-001: invalid Kind "no tagged test or waiver"`,
		`REQ-CORE-001: invalid Kind "policy requires integration coverage"`,
		`REQ-CORE-001: invalid classification "value but has no integration coverage"`,
		`REQ-CORE-001: invalid rationale "waiver reason not-implemented does not satisfy strict coverage"`,
	}
	for _, message := range messages {
		if got := cfg.classifyProblem(message); got.Code != "integrity" || got.Baselinable {
			t.Errorf("integrity text misclassified: %+v", got)
		}
	}
}

func TestProblemClassificationSupportsCapturingIDGrammar(t *testing.T) {
	cfg := Default()
	cfg.IDGrammar.Pattern = `REQ-(?P<class>[A-Z]+)-\d+`
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	got := cfg.classifyProblem("REQ-CORE-1 (§1): policy requires integration coverage")
	if got.Key != "coverage-class-required:REQ-CORE-1:integration" {
		t.Fatalf("capturing ID grammar shifted diagnostic fields: %+v", got)
	}
	got = cfg.classifyProblem("REQ-CORE-1 (§1): covered-by target REQ-API-2 has no tagged test for strict coverage")
	if got.Key != "waiver-proxy-not-covered:REQ-CORE-1:REQ-API-2" {
		t.Fatalf("named ID capture shifted target field: %+v", got)
	}
}

func TestProblemClassificationUsesTerminalDiagnosticSuffix(t *testing.T) {
	cfg := cfgT(t)
	tests := []struct {
		message string
		key     string
	}{
		{
			message: "REQ-CORE-001 (§1: policy requires unit coverage): policy requires integration coverage",
			key:     "coverage-class-required:REQ-CORE-001:integration",
		},
		{
			message: `REQ-CORE-001 (§1: waiver reason "other" does not satisfy strict coverage): waiver reason "not-implemented" does not satisfy strict coverage`,
			key:     "waiver-reason-not-accepted:REQ-CORE-001:not-implemented",
		},
		{
			message: "REQ-CORE-001 (§1: covered-by target REQ-CORE-003 has no tagged test for strict coverage): covered-by target REQ-CORE-002 has no tagged test for strict coverage",
			key:     "waiver-proxy-not-covered:REQ-CORE-001:REQ-CORE-002",
		},
	}
	for _, tt := range tests {
		if got := cfg.classifyProblem(tt.message); got.Key != tt.key || !got.Baselinable {
			t.Errorf("classifyProblem(%q) = %+v, want key %q", tt.message, got, tt.key)
		}
	}
}

func TestProblemReportSortsByStableKey(t *testing.T) {
	cfg := cfgT(t)
	report, err := cfg.problemReport(Scope{Profile: "freeze"}, []string{
		"REQ-CORE-002 (MUST, §1): no tagged test or waiver",
		"REQ-CORE-001 (MUST, §1): no tagged test or waiver",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 2 || report.Problems[0].Requirement != "REQ-CORE-001" || report.Problems[1].Requirement != "REQ-CORE-002" {
		t.Fatalf("unsorted report: %+v", report.Problems)
	}
}

func TestProblemReportDeduplicatesStableKeys(t *testing.T) {
	cfg := cfgT(t)
	message := "REQ-CORE-001 (§1): policy requires integration coverage"
	report, err := cfg.problemReport(Scope{Strict: true}, []string{message, message}, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Total != 1 || len(report.Problems) != 1 {
		t.Fatalf("duplicate keys retained: %+v", report)
	}
}

func TestProblemEvidenceExcludesTagsFromOtherCatalogSeries(t *testing.T) {
	cfg := cfgT(t)
	tags := map[string][]TagRef{
		"REQ-CORE-001":   {{File: "core_test.go", Func: "TestCore", Class: "unit"}},
		"REQ-CORE-999":   {{File: "unknown_test.go", Func: "TestUnknown", Class: "unit"}},
		"REQ-SERVER-001": {{File: "server_test.go", Func: "TestServer", Class: "unit"}},
	}
	filtered := cfg.tagsForCatalogSeries(tags, map[string]bool{"CORE": true})
	evidence, err := problemEvidence(filtered)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Tags) != 2 || evidence.Tags[0].Requirement != "REQ-CORE-001" || evidence.Tags[1].Requirement != "REQ-CORE-999" {
		t.Fatalf("catalog-scoped evidence = %+v", evidence.Tags)
	}
}
