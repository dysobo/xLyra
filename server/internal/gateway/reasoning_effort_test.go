package gateway

import (
	"slices"
	"strings"
	"testing"

	routeengine "xlyra/server/internal/router"
	"xlyra/server/internal/store"
)

func TestSnapReasoningEffort(t *testing.T) {
	t.Parallel()

	threeLevels := []string{"low", "high", "max"}
	tests := []struct {
		name      string
		requested string
		levels    []string
		want      string
	}{
		{name: "exact match passes through", requested: "high", levels: threeLevels, want: "high"},
		{name: "below range clamps to lowest", requested: "none", levels: threeLevels, want: "low"},
		{name: "minimal clamps to lowest", requested: "minimal", levels: threeLevels, want: "low"},
		{name: "missing tier snaps to nearest", requested: "xhigh", levels: threeLevels, want: "max"},
		{name: "above range clamps to highest", requested: "ultra", levels: threeLevels, want: "max"},
		{name: "medium ties upward", requested: "medium", levels: threeLevels, want: "high"},
		{name: "closer lower tier wins", requested: "medium", levels: []string{"low", "max"}, want: "low"},
		{name: "unknown requested value rejected", requested: "bogus", levels: threeLevels, want: ""},
		{name: "empty levels rejected", requested: "high", levels: nil, want: ""},
		{name: "none kept when supported", requested: "none", levels: []string{"none", "high", "max"}, want: "none"},
		{name: "full ladder is identity", requested: "xhigh", levels: []string{"none", "minimal", "low", "medium", "high", "xhigh"}, want: "xhigh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snapReasoningEffort(tt.requested, tt.levels); got != tt.want {
				t.Fatalf("snapReasoningEffort(%q, %v) = %q, want %q", tt.requested, tt.levels, got, tt.want)
			}
		})
	}
}

func TestValidateReasoningEffortSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spec    *reasoningEffortSpec
		wantErr string
	}{
		{name: "nil spec is valid", spec: nil},
		{name: "snap mode valid", spec: &reasoningEffortSpec{Levels: []string{"low", "high"}}},
		{name: "passthrough mode valid", spec: &reasoningEffortSpec{Mode: "passthrough", Levels: []string{"low"}}},
		{name: "unknown mode rejected", spec: &reasoningEffortSpec{Mode: "rewrite", Levels: []string{"low"}}, wantErr: "mode"},
		{name: "empty levels rejected", spec: &reasoningEffortSpec{Mode: "snap"}, wantErr: "at least one level"},
		{name: "unknown level rejected", spec: &reasoningEffortSpec{Levels: []string{"low", "turbo"}}, wantErr: "not a canonical effort"},
		{name: "duplicate level rejected", spec: &reasoningEffortSpec{Levels: []string{"low", "LOW"}}, wantErr: "duplicated"},
		{name: "mandatory thinking conflicts with none", spec: &reasoningEffortSpec{Levels: []string{"none", "low"}, ThinkingMandatory: true}, wantErr: "mandatory"},
		{name: "default within levels valid", spec: &reasoningEffortSpec{Levels: []string{"low", "high"}, Default: "high"}},
		{name: "unknown default rejected", spec: &reasoningEffortSpec{Levels: []string{"low"}, Default: "turbo"}, wantErr: "not a canonical effort"},
		{name: "default outside levels rejected", spec: &reasoningEffortSpec{Levels: []string{"low", "high"}, Default: "max"}, wantErr: "not in levels"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReasoningEffortSpec(tt.spec)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateReasoningEffortSpec returned unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateReasoningEffortSpec error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestApplyReasoningEffortPolicySnapsKnownLocations(t *testing.T) {
	t.Parallel()

	policy := &reasoningEffortSpec{Levels: []string{"low", "high", "max"}}
	tests := []struct {
		name    string
		payload map[string]any
		check   func(t *testing.T, payload map[string]any)
	}{
		{
			name:    "top-level reasoning_effort",
			payload: map[string]any{"reasoning_effort": "medium"},
			check: func(t *testing.T, payload map[string]any) {
				if got := payload["reasoning_effort"]; got != "high" {
					t.Fatalf("reasoning_effort = %v, want high", got)
				}
			},
		},
		{
			name:    "nested reasoning.effort",
			payload: map[string]any{"reasoning": map[string]any{"effort": "xhigh", "summary": "detailed"}},
			check: func(t *testing.T, payload map[string]any) {
				reasoning := payload["reasoning"].(map[string]any)
				if got := reasoning["effort"]; got != "max" {
					t.Fatalf("reasoning.effort = %v, want max", got)
				}
				if got := reasoning["summary"]; got != "detailed" {
					t.Fatalf("reasoning.summary was modified: %v", got)
				}
			},
		},
		{
			name:    "nested output_config.effort",
			payload: map[string]any{"output_config": map[string]any{"effort": "ultra"}},
			check: func(t *testing.T, payload map[string]any) {
				if got := payload["output_config"].(map[string]any)["effort"]; got != "max" {
					t.Fatalf("output_config.effort = %v, want max", got)
				}
			},
		},
		{
			name:    "supported value untouched",
			payload: map[string]any{"reasoning_effort": "high"},
			check: func(t *testing.T, payload map[string]any) {
				if got := payload["reasoning_effort"]; got != "high" {
					t.Fatalf("reasoning_effort = %v, want high", got)
				}
			},
		},
		{
			name:    "payload without effort untouched",
			payload: map[string]any{"model": "kimi-k3"},
			check: func(t *testing.T, payload map[string]any) {
				if _, ok := payload["reasoning_effort"]; ok {
					t.Fatalf("reasoning_effort injected unexpectedly: %v", payload)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := applyReasoningEffortPolicy(tt.payload, resolvedProtocolSpec{ReasoningEffort: policy})
			tt.check(t, out)
		})
	}
}

func TestApplyReasoningEffortPolicyPassthroughMode(t *testing.T) {
	t.Parallel()

	spec := resolvedProtocolSpec{ReasoningEffort: &reasoningEffortSpec{
		Levels: []string{"none", "low", "high", "max"},
		Mode:   "passthrough",
	}}
	payload := map[string]any{"reasoning_effort": "medium"}
	out := applyReasoningEffortPolicy(payload, spec)
	if got := out["reasoning_effort"]; got != "medium" {
		t.Fatalf("passthrough mode must not snap, reasoning_effort = %v", got)
	}
}

func TestApplyReasoningEffortPolicyWithoutSpec(t *testing.T) {
	t.Parallel()

	payload := map[string]any{"reasoning_effort": "medium"}
	out := applyReasoningEffortPolicy(payload, resolvedProtocolSpec{})
	if got := out["reasoning_effort"]; got != "medium" {
		t.Fatalf("missing spec must leave payload untouched, reasoning_effort = %v", got)
	}
}

func TestApplyReasoningEffortPolicyMandatoryThinking(t *testing.T) {
	t.Parallel()

	spec := resolvedProtocolSpec{ReasoningEffort: &reasoningEffortSpec{
		Levels:            []string{"low", "high", "max"},
		ThinkingMandatory: true,
	}}

	t.Run("disabled thinking flipped to enabled, falls back to lowest without a default", func(t *testing.T) {
		payload := map[string]any{"thinking": map[string]any{"type": "disabled"}}
		out := applyReasoningEffortPolicy(payload, spec)
		thinking := out["thinking"].(map[string]any)
		if got := thinking["type"]; got != "enabled" {
			t.Fatalf("thinking.type = %v, want enabled", got)
		}
		if got := out["reasoning_effort"]; got != "low" {
			t.Fatalf("reasoning_effort = %v, want low", got)
		}
	})

	t.Run("disabled thinking maps to the configured default", func(t *testing.T) {
		kimiLike := resolvedProtocolSpec{ReasoningEffort: &reasoningEffortSpec{
			Levels:            []string{"low", "high", "max"},
			ThinkingMandatory: true,
			Default:           "max",
		}}
		out := applyReasoningEffortPolicy(map[string]any{"thinking": map[string]any{"type": "disabled"}}, kimiLike)
		if got := out["reasoning_effort"]; got != "max" {
			t.Fatalf("reasoning_effort = %v, want max (configured default)", got)
		}
		out = applyReasoningEffortPolicy(map[string]any{"reasoning_effort": "none"}, kimiLike)
		if got := out["reasoning_effort"]; got != "max" {
			t.Fatalf("reasoning_effort = %v, want max (none maps to default)", got)
		}
	})

	t.Run("bool false thinking flipped", func(t *testing.T) {
		payload := map[string]any{"thinking": false, "reasoning_effort": "high"}
		out := applyReasoningEffortPolicy(payload, spec)
		thinking, ok := out["thinking"].(map[string]any)
		if !ok || thinking["type"] != "enabled" {
			t.Fatalf("thinking = %v, want {type: enabled}", out["thinking"])
		}
		if got := out["reasoning_effort"]; got != "high" {
			t.Fatalf("explicit effort must be preserved, reasoning_effort = %v", got)
		}
	})

	t.Run("none effort clamped to lowest", func(t *testing.T) {
		payload := map[string]any{"reasoning_effort": "none"}
		out := applyReasoningEffortPolicy(payload, spec)
		if got := out["reasoning_effort"]; got != "low" {
			t.Fatalf("reasoning_effort = %v, want low", got)
		}
	})

	t.Run("enabled thinking untouched", func(t *testing.T) {
		payload := map[string]any{"thinking": map[string]any{"type": "enabled"}}
		out := applyReasoningEffortPolicy(payload, spec)
		if _, ok := out["reasoning_effort"]; ok {
			t.Fatalf("reasoning_effort injected unexpectedly: %v", out)
		}
	})
}

// TestReasoningEffortModelInheritance guards the core maintenance contract:
// new models in a known family inherit the family's effort levels via glob
// matching, and only models that redefine levels need their own entry.
func TestReasoningEffortModelInheritance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		siteType  string
		model     string
		wantFirst string
		wantLast  string
		mandatory bool
	}{
		{name: "future kimi-k3 minor inherits k3 levels", siteType: "moonshot", model: "kimi-k3.1", wantFirst: "low", wantLast: "max", mandatory: true},
		{name: "kimi code short name", siteType: "kimi_code", model: "k3", wantFirst: "low", wantLast: "max", mandatory: true},
		{name: "glm-5.3 thinking locked", siteType: "zhipu", model: "glm-5.3", wantFirst: "low", wantLast: "max", mandatory: true},
		{name: "glm-5.2 keeps none", siteType: "zhipu", model: "glm-5.2-air", wantFirst: "none", wantLast: "max", mandatory: false},
		{name: "claude opus 4.6 lacks xhigh", siteType: "anthropic", model: "claude-opus-4-6", wantFirst: "low", wantLast: "max", mandatory: false},
		{name: "claude opus 4.7 full ladder", siteType: "anthropic", model: "claude-opus-4-7", wantFirst: "low", wantLast: "max", mandatory: false},
		{name: "claude opus 4.5 effort debut", siteType: "anthropic", model: "claude-opus-4-5-20251101", wantFirst: "low", wantLast: "high", mandatory: false},
		{name: "future claude opus 5 minor inherits", siteType: "anthropic", model: "claude-opus-5.1", wantFirst: "low", wantLast: "max", mandatory: false},
		{name: "gpt-6 drops none", siteType: "openai", model: "gpt-6-luna", wantFirst: "minimal", wantLast: "max", mandatory: true},
		{name: "gpt-5.6 adds max", siteType: "openai", model: "gpt-5.6-luna", wantFirst: "none", wantLast: "max", mandatory: false},
		{name: "gpt-5.6-sol exact entry adds ultra", siteType: "openai", model: "gpt-5.6-sol", wantFirst: "none", wantLast: "ultra", mandatory: false},
		{name: "gpt-5.6-terra exact entry adds ultra", siteType: "openai", model: "gpt-5.6-terra", wantFirst: "none", wantLast: "ultra", mandatory: false},
		{name: "gpt-6-astra exact entry adds ultra", siteType: "openai", model: "gpt-6-astra", wantFirst: "minimal", wantLast: "ultra", mandatory: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := routeengine.Candidate{
				Site:  routeengine.CandidateSite{SiteType: tt.siteType},
				Model: routeengine.CandidateModel{UpstreamName: tt.model},
			}
			spec := effectiveProtocolSpec(canonicalProtocolOpenAIChat, candidate)
			policy := spec.ReasoningEffort
			if policy == nil {
				t.Fatalf("model %q resolved without reasoning_effort spec (pattern %q)", tt.model, spec.ModelPattern)
			}
			if got := policy.Levels[0]; got != tt.wantFirst {
				t.Fatalf("model %q first level = %q, want %q (levels %v)", tt.model, got, tt.wantFirst, policy.Levels)
			}
			if got := policy.Levels[len(policy.Levels)-1]; got != tt.wantLast {
				t.Fatalf("model %q last level = %q, want %q (levels %v)", tt.model, got, tt.wantLast, policy.Levels)
			}
			if policy.ThinkingMandatory != tt.mandatory {
				t.Fatalf("model %q thinking_mandatory = %v, want %v", tt.model, policy.ThinkingMandatory, tt.mandatory)
			}
		})
	}
}

func TestReasoningEffortModelInheritanceDistinguishesXhigh(t *testing.T) {
	t.Parallel()

	hasXhigh := func(model string) bool {
		spec := effectiveProtocolSpec(canonicalProtocolAnthropicMessages, routeengine.Candidate{
			Site:  routeengine.CandidateSite{SiteType: "anthropic"},
			Model: routeengine.CandidateModel{UpstreamName: model},
		})
		if spec.ReasoningEffort == nil {
			return false
		}
		return slices.Contains(spec.ReasoningEffort.Levels, "xhigh")
	}

	if hasXhigh("claude-opus-4-6") {
		t.Fatalf("claude-opus-4-6 must not advertise xhigh")
	}
	if hasXhigh("claude-sonnet-4-6") {
		t.Fatalf("claude-sonnet-4-6 must not advertise xhigh")
	}
	if !hasXhigh("claude-opus-4-7") {
		t.Fatalf("claude-opus-4-7 must advertise xhigh")
	}
	if !hasXhigh("claude-sonnet-5") {
		t.Fatalf("claude-sonnet-5 must advertise xhigh")
	}
}

func TestApplyRequestPolicySnapsEffortEndToEnd(t *testing.T) {
	t.Parallel()

	t.Run("medium snapped to high for kimi-k3", func(t *testing.T) {
		candidate := routeengine.Candidate{
			Site:  routeengine.CandidateSite{SiteType: "moonshot"},
			Model: routeengine.CandidateModel{UpstreamName: "kimi-k3"},
		}
		payload := applyRequestPolicyForCandidate(map[string]any{
			"model":            "kimi-k3",
			"reasoning_effort": "medium",
		}, canonicalProtocolOpenAIChat, candidate)
		if got := payload["reasoning_effort"]; got != "high" {
			t.Fatalf("reasoning_effort = %v, want high", got)
		}
	})

	t.Run("deepseek passes canonical values through", func(t *testing.T) {
		candidate := routeengine.Candidate{
			Site:  routeengine.CandidateSite{SiteType: "deepseek"},
			Model: routeengine.CandidateModel{UpstreamName: "deepseek-v4-pro"},
		}
		payload := applyRequestPolicyForCandidate(map[string]any{
			"model":            "deepseek-v4-pro",
			"reasoning_effort": "medium",
		}, canonicalProtocolOpenAIChat, candidate)
		if got := payload["reasoning_effort"]; got != "medium" {
			t.Fatalf("reasoning_effort = %v, want medium (upstream self-snaps)", got)
		}
	})

	t.Run("xhigh snapped to max for claude-opus-4-6", func(t *testing.T) {
		candidate := routeengine.Candidate{
			Site:  routeengine.CandidateSite{SiteType: "anthropic"},
			Model: routeengine.CandidateModel{UpstreamName: "claude-opus-4-6"},
		}
		payload := applyRequestPolicyForCandidate(map[string]any{
			"model":         "claude-opus-4-6",
			"output_config": map[string]any{"effort": "xhigh"},
		}, canonicalProtocolAnthropicMessages, candidate)
		outputConfig, ok := payload["output_config"].(map[string]any)
		if !ok {
			t.Fatalf("output_config missing after policy: %v", payload)
		}
		if got := outputConfig["effort"]; got != "max" {
			t.Fatalf("output_config.effort = %v, want max", got)
		}
	})

	t.Run("none maps to the default level for gpt-6", func(t *testing.T) {
		candidate := routeengine.Candidate{
			Site:  routeengine.CandidateSite{SiteType: "openai"},
			Model: routeengine.CandidateModel{UpstreamName: "gpt-6-astra"},
		}
		payload := applyRequestPolicyForCandidate(map[string]any{
			"model":            "gpt-6-astra",
			"reasoning_effort": "none",
		}, canonicalProtocolOpenAIChat, candidate)
		if got := payload["reasoning_effort"]; got != "medium" {
			t.Fatalf("reasoning_effort = %v, want medium (gpt-6 default)", got)
		}
	})

	t.Run("unknown model left untouched", func(t *testing.T) {
		candidate := routeengine.Candidate{
			Site:  routeengine.CandidateSite{SiteType: "moonshot"},
			Model: routeengine.CandidateModel{UpstreamName: "kimi-k9-experimental"},
		}
		payload := applyRequestPolicyForCandidate(map[string]any{
			"model":            "kimi-k9-experimental",
			"reasoning_effort": "medium",
		}, canonicalProtocolOpenAIChat, candidate)
		if got := payload["reasoning_effort"]; got != "medium" {
			t.Fatalf("unknown model must pass through, reasoning_effort = %v", got)
		}
	})
}

func TestReasoningEffortSpecForModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		modelKey  string
		want      []string
		mode      string
		mandatory bool
		def       string
	}{
		{name: "kimi k3 declared levels", modelKey: "kimi-k3", want: []string{"low", "high", "max"}, mandatory: true, def: "max"},
		{name: "future kimi minor inherits", modelKey: "kimi-k3.1", want: []string{"low", "high", "max"}, mandatory: true, def: "max"},
		{name: "claude opus 4.6 lacks xhigh", modelKey: "claude-opus-4-6", want: []string{"low", "medium", "high", "max"}, def: "high"},
		{name: "claude fable 5 full ladder", modelKey: "claude-fable-5", want: []string{"low", "medium", "high", "xhigh", "max"}, mandatory: true, def: "high"},
		{name: "deepseek passthrough", modelKey: "deepseek-v4-flash", want: []string{"none", "low", "high", "max"}, mode: "passthrough", def: "high"},
		{name: "glm 5.3 mandatory", modelKey: "glm-5.3", want: []string{"low", "high", "max"}, mandatory: true, def: "max"},
		{name: "gpt 6 floor at minimal", modelKey: "gpt-6", want: []string{"minimal", "low", "medium", "high", "xhigh", "max"}, mandatory: true, def: "medium"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ReasoningEffortSpecForModel(tt.modelKey)
			if info == nil {
				t.Fatalf("ReasoningEffortSpecForModel(%q) = nil, want levels %v", tt.modelKey, tt.want)
			}
			if !slices.Equal(info.Levels, tt.want) {
				t.Fatalf("ReasoningEffortSpecForModel(%q).Levels = %v, want %v (sorted by rank)", tt.modelKey, info.Levels, tt.want)
			}
			if info.Mode != tt.mode {
				t.Fatalf("ReasoningEffortSpecForModel(%q).Mode = %q, want %q", tt.modelKey, info.Mode, tt.mode)
			}
			if info.ThinkingMandatory != tt.mandatory {
				t.Fatalf("ReasoningEffortSpecForModel(%q).ThinkingMandatory = %v, want %v", tt.modelKey, info.ThinkingMandatory, tt.mandatory)
			}
			if info.Default != tt.def {
				t.Fatalf("ReasoningEffortSpecForModel(%q).Default = %q, want %q", tt.modelKey, info.Default, tt.def)
			}
		})
	}

	t.Run("undeclared model returns nil", func(t *testing.T) {
		if info := ReasoningEffortSpecForModel("some-unheard-of-model"); info != nil {
			t.Fatalf("ReasoningEffortSpecForModel(unknown) = %+v, want nil", info)
		}
	})
}

func TestCanonicalModelPayloadExposesReasoningEffort(t *testing.T) {
	t.Parallel()

	payload := canonicalModelPayload(store.CanonicalModel{ModelKey: "kimi-k3"})
	metadata, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing from payload: %v", payload)
	}
	effort, ok := metadata["reasoning_effort"].(*ReasoningEffortInfo)
	if !ok {
		t.Fatalf("metadata.reasoning_effort missing or wrong type: %v", metadata)
	}
	if !slices.Equal(effort.Levels, []string{"low", "high", "max"}) {
		t.Fatalf("levels = %v, want [low high max]", effort.Levels)
	}
	if !effort.ThinkingMandatory {
		t.Fatalf("thinking_mandatory = false, want true")
	}

	unknown := canonicalModelPayload(store.CanonicalModel{ModelKey: "some-unheard-of-model"})
	if metadata, ok := unknown["metadata"].(map[string]any); ok {
		if _, present := metadata["reasoning_effort"]; present {
			t.Fatalf("unknown model must not carry reasoning_effort: %v", metadata)
		}
	}
}

func TestApplyReasoningEffortPolicyResolvesAuto(t *testing.T) {
	t.Parallel()

	t.Run("auto dropped for models that accept an omitted effort", func(t *testing.T) {
		spec := resolvedProtocolSpec{ReasoningEffort: &reasoningEffortSpec{
			Levels:  []string{"none", "minimal", "low", "medium", "high", "xhigh"},
			Default: "medium",
		}}
		payload := map[string]any{"reasoning_effort": "auto"}
		out := applyReasoningEffortPolicy(payload, spec)
		if _, present := out["reasoning_effort"]; present {
			t.Fatalf("auto must be omitted for non-mandatory models, got %v", out["reasoning_effort"])
		}
	})

	t.Run("auto maps to the configured default for mandatory models", func(t *testing.T) {
		spec := resolvedProtocolSpec{ReasoningEffort: &reasoningEffortSpec{
			Levels:            []string{"low", "high", "max"},
			ThinkingMandatory: true,
			Default:           "max",
		}}
		out := applyReasoningEffortPolicy(map[string]any{"reasoning_effort": "AUTO"}, spec)
		if got := out["reasoning_effort"]; got != "max" {
			t.Fatalf("reasoning_effort = %v, want max", got)
		}
	})

	t.Run("auto falls back to the lowest level without a configured default", func(t *testing.T) {
		spec := resolvedProtocolSpec{ReasoningEffort: &reasoningEffortSpec{
			Levels:            []string{"low", "high", "max"},
			ThinkingMandatory: true,
		}}
		out := applyReasoningEffortPolicy(map[string]any{"reasoning_effort": "auto"}, spec)
		if got := out["reasoning_effort"]; got != "low" {
			t.Fatalf("reasoning_effort = %v, want low", got)
		}
	})

	t.Run("auto dropped even in passthrough mode", func(t *testing.T) {
		spec := resolvedProtocolSpec{ReasoningEffort: &reasoningEffortSpec{
			Levels: []string{"none", "low", "high", "max"},
			Mode:   "passthrough",
		}}
		out := applyReasoningEffortPolicy(map[string]any{"reasoning_effort": "auto"}, spec)
		if _, present := out["reasoning_effort"]; present {
			t.Fatalf("auto must not leak to a passthrough upstream, got %v", out["reasoning_effort"])
		}
	})

	t.Run("auto dropped without any spec", func(t *testing.T) {
		out := applyReasoningEffortPolicy(map[string]any{"reasoning_effort": "auto"}, resolvedProtocolSpec{})
		if _, present := out["reasoning_effort"]; present {
			t.Fatalf("auto must not leak for undeclared models, got %v", out["reasoning_effort"])
		}
	})

	t.Run("auto resolved in nested locations", func(t *testing.T) {
		spec := resolvedProtocolSpec{ReasoningEffort: &reasoningEffortSpec{
			Levels:            []string{"low", "high", "max"},
			ThinkingMandatory: true,
			Default:           "high",
		}}
		out := applyReasoningEffortPolicy(map[string]any{
			"reasoning":     map[string]any{"effort": "auto"},
			"output_config": map[string]any{"effort": "high"},
		}, spec)
		if got := out["reasoning"].(map[string]any)["effort"]; got != "high" {
			t.Fatalf("reasoning.effort = %v, want high (default)", got)
		}
		if got := out["output_config"].(map[string]any)["effort"]; got != "high" {
			t.Fatalf("output_config.effort = %v, want high (untouched)", got)
		}
	})
}
