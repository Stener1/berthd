package box

import "testing"

func TestOmpIsABuiltinAgentThatRunsOverACP(t *testing.T) {
	var omp *AgentPreset
	for i := range builtinAgents {
		if builtinAgents[i].ID == "omp" {
			omp = &builtinAgents[i]
		}
	}
	if omp == nil {
		t.Fatal("omp is not a built-in agent")
	}
	if omp.Command != "omp" || omp.ACP != "acp" || omp.ModelFlag != "--model" || omp.EffortFlag != "--thinking" {
		t.Fatalf("preset = %+v", *omp)
	}
}

func TestAnOmpCommandIsRecognisedAsOmp(t *testing.T) {
	for command, want := range map[string]string{
		"omp":                                 "omp",
		"env FOO=1 omp --model opus":          "omp",
		"/Users/a/.local/bin/omp -r":          "omp",
		"bunx @oh-my-pi/pi-coding-agent":      "omp",
		"npx @oh-my-pi/pi-coding-agent@1.2.3": "omp",
	} {
		if got := agentOf(command); got != want {
			t.Errorf("agentOf(%q) = %q, want %q", command, got, want)
		}
	}
}
