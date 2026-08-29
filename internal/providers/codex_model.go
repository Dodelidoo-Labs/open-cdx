package providers

import (
	"encoding/json"
	"fmt"
)

// Third-party catalog entries need an instruction-template field to satisfy the
// Codex catalog schema. Keep it empty: the router must not author, inject, or
// rewrite a provider prompt.
const routerAuthoredInstructions = ""

type CodexModelOptions struct {
	Slug                  string
	DisplayName           string
	Description           string
	Context               int64
	Priority              int
	ImageInput            bool
	EnableApplyPatch      bool
	ReasoningLevels       []ReasoningLevel
	DefaultReasoningLevel string
	SupportsVerbosity     bool
}

type ReasoningLevel struct {
	Effort      string
	Description string
}

func BuildCodexModel(options CodexModelOptions) (json.RawMessage, error) {
	inputModalities := []string{"text"}
	if options.ImageInput {
		inputModalities = append(inputModalities, "image")
	}
	reasoningLevels := make([]map[string]string, 0, len(options.ReasoningLevels))
	for _, level := range options.ReasoningLevels {
		if level.Effort == "" {
			continue
		}
		reasoningLevels = append(reasoningLevels, map[string]string{
			"effort": level.Effort, "description": level.Description,
		})
	}
	var defaultReasoningLevel any
	if options.DefaultReasoningLevel != "" {
		defaultReasoningLevel = options.DefaultReasoningLevel
	}
	var applyPatchToolType any
	if options.EnableApplyPatch {
		// Codex executes this tool locally. `freeform` describes the Responses
		// wire shape accepted by the destination; it does not ask the provider
		// to edit the user's filesystem.
		applyPatchToolType = "freeform"
	}
	model := map[string]any{
		"slug":                                 options.Slug,
		"display_name":                         options.DisplayName,
		"description":                          options.Description,
		"default_reasoning_level":              defaultReasoningLevel,
		"supported_reasoning_levels":           reasoningLevels,
		"shell_type":                           "unified_exec",
		"visibility":                           "list",
		"supported_in_api":                     true,
		"priority":                             options.Priority,
		"availability_nux":                     nil,
		"upgrade":                              nil,
		"model_messages":                       map[string]any{"instructions_template": routerAuthoredInstructions, "instructions_variables": nil, "approvals": nil, "collaboration_modes": nil, "auto_review": nil, "permissions": nil, "multi_agent": nil},
		"base_instructions":                    routerAuthoredInstructions,
		"include_skills_usage_instructions":    false,
		"include_plugin_usage_instructions":    false,
		"include_apps_usage_instructions":      false,
		"supports_reasoning_summary_parameter": false,
		"default_reasoning_summary":            "none",
		"support_verbosity":                    options.SupportsVerbosity,
		"default_verbosity":                    nil,
		"apply_patch_tool_type":                applyPatchToolType,
		// Codex's schema currently has no disabled web-search enum value. `text`
		// avoids the optional search_content_types parameter; destination request
		// normalization is responsible for removing hosted search where the
		// provider cannot execute it. supports_search_tool controls Codex's
		// deferred tool-search facility, not hosted web search.
		"web_search_tool_type":             "text",
		"truncation_policy":                map[string]any{"mode": "bytes", "limit": 10000},
		"supports_image_detail_original":   false,
		"context_window":                   options.Context,
		"max_context_window":               options.Context,
		"effective_context_window_percent": 90,
		"experimental_supported_tools":     []any{},
		"input_modalities":                 inputModalities,
		"supports_search_tool":             false,
		"use_responses_lite":               false,
		"node_repl_auto_review_required":   false,
		"node_repl_disabled":               false,
		"auto_review_model_override":       nil,
		"model_specialty":                  nil,
		"tool_mode":                        nil,
		"multi_agent_version":              nil,
		"multi_agent_reasoning_effort":     nil,
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode Codex catalog model: %w", err)
	}
	return encoded, nil
}
