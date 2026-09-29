package diag

import "testing"

// TestRuntimeFindings_Classify proves that repeat signatures are classified by shape and the thresholds escalate correctly,
// and that every runtime Finding is AutoNone (the observer discipline: diagnose only, produce no Action).
func TestRuntimeFindings_Classify(t *testing.T) {
	rc := RuntimeCapture{
		Repeats: []RepeatStat{
			{Sig: "writer-ch07 · err: InputValidationError", Count: 14}, // {Sig: "writer-ch07 · err: InputValidationError", Count: 14}, // an error loop is critical
			{Sig: "writer-ch07 · novel_context", Count: 45},             // {Sig: "writer-ch07 · novel_context", Count: 45},             // a normal high-frequency tool -> no Finding
			{Sig: "writer · save_plan (args invalid)", Count: 4},        // {Sig: "writer · save_plan (args invalid)", Count: 4},        // invalid argument is a warning
		},
		StuckStep:  "writing.commit_ch07",
		StuckCount: 9, // StuckCount: 9, // stuck is critical
		LogKinds:   map[string]int{"stream_idle": 4},
		LogErrors:  270, // LogErrors:  270, // a long-run accumulation, it must not produce a Finding on its own
	}

	fs := runtimeFindings(&rc)
	sev := map[string]Severity{}
	for _, f := range fs {
		sev[f.Rule] = f.Severity
		if f.AutoLevel != AutoNone {
			t.Errorf("%s 应为 AutoNone（观察者纪律），got %s", f.Rule, f.AutoLevel)
		}
	}

	want := map[string]Severity{
		"RepeatedToolError": SevCritical,
		"ArgsInvalidLoop":   SevWarning,
		"StuckStep":         SevCritical,
		"StreamIdleStorm":   SevWarning,
	}
	for rule, w := range want {
		if sev[rule] != w {
			t.Errorf("%s: got %q want %q", rule, sev[rule], w)
		}
	}
	// an ordinary high-frequency tool / an accumulated log error must not produce a Finding (avoiding a long-run false positive).
	if _, ok := sev["RepeatedToolCall"]; ok {
		t.Error("普通工具重复不应产 Finding")
	}
	if _, ok := sev["LogErrorBurst"]; ok {
		t.Error("日志 error 累计不应单独产 Finding")
	}
}

// TestRuntimeFindings_Quiet proves that no runtime Finding is produced when there is no anomaly signal (zero false positives).
func TestRuntimeFindings_Quiet(t *testing.T) {
	rc := RuntimeCapture{
		LogKinds:  map[string]int{"stream_idle": 1}, // LogKinds:  map[string]int{"stream_idle": 1}, // below the threshold
		LogErrors: 2,
	}
	if fs := runtimeFindings(&rc); len(fs) != 0 {
		t.Errorf("安静态不应产 Finding，got %d: %+v", len(fs), fs)
	}
}
