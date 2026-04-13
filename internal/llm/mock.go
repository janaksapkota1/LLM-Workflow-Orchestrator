package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MockClient simulates LLM responses without any API calls.
// It returns realistic-looking fake output so you can test the full
// pipeline (decomposition → step execution → workflow completion)
// without an Anthropic API key.
type MockClient struct {
	// DelayMs simulates network latency per call (default 300ms).
	DelayMs int
}

// NewMock creates a MockClient with a sensible default delay.
func NewMock() *MockClient {
	return &MockClient{DelayMs: 300}
}

// Complete returns a mock response based on keywords in the prompt.
func (m *MockClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (*CompletionResult, error) {
	// Simulate latency
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Duration(m.DelayMs) * time.Millisecond):
	}

	text := m.generateResponse(systemPrompt, userPrompt)
	return &CompletionResult{
		Text:         text,
		InputTokens:  len(strings.Fields(userPrompt)),
		OutputTokens: len(strings.Fields(text)),
	}, nil
}

// CompleteWithHistory delegates to Complete using the last user message.
func (m *MockClient) CompleteWithHistory(ctx context.Context, systemPrompt string, history []interface{}, userPrompt string) (*CompletionResult, error) {
	return m.Complete(ctx, systemPrompt, userPrompt)
}

// generateResponse returns appropriate mock content based on prompt keywords.
func (m *MockClient) generateResponse(systemPrompt, userPrompt string) string {
	lower := strings.ToLower(userPrompt)

	// ── Task decomposition ────────────────────────────────────────────────────
	// The orchestrator calls Complete() with a system prompt about decomposing tasks.
	if strings.Contains(strings.ToLower(systemPrompt), "decompose") ||
		strings.Contains(strings.ToLower(systemPrompt), "workflow planning") {
		return m.mockDecomposition(userPrompt)
	}

	// ── Step execution responses ──────────────────────────────────────────────
	switch {
	case strings.Contains(lower, "research") || strings.Contains(lower, "find information"):
		return fmt.Sprintf("## Research Results\n\nAfter thorough research on the topic '%s':\n\n"+
			"1. Key finding: This is a well-established area with significant literature.\n"+
			"2. Main concepts include foundational principles, practical applications, and recent developments.\n"+
			"3. Notable sources confirm the importance of structured approaches.\n"+
			"4. Current trends point toward increased automation and integration.\n\n"+
			"These findings provide a solid foundation for the next steps.", truncate(userPrompt, 60))

	case strings.Contains(lower, "analyz") || strings.Contains(lower, "structur"):
		return "## Analysis\n\nBased on the provided information:\n\n" +
			"**Strengths:** Clear structure, well-defined scope, actionable insights.\n" +
			"**Weaknesses:** Some areas require deeper investigation.\n" +
			"**Opportunities:** Automation and tooling can significantly improve outcomes.\n" +
			"**Recommendations:** Prioritize the top 3 findings for immediate action.\n\n" +
			"The structured analysis reveals a clear path forward."

	case strings.Contains(lower, "write") || strings.Contains(lower, "draft") || strings.Contains(lower, "blog"):
		return "## Draft Output\n\n" +
			"### Introduction\nThis topic represents a significant area of interest for practitioners and researchers alike.\n\n" +
			"### Main Body\nThe core principles can be summarized in three key areas:\n" +
			"- **First principle**: Establish clear foundations before building complexity.\n" +
			"- **Second principle**: Iterate based on feedback and real-world results.\n" +
			"- **Third principle**: Measure outcomes against well-defined success criteria.\n\n" +
			"### Conclusion\nBy following these principles, teams can achieve reliable and repeatable results.\n\n" +
			"*[Mock draft — replace with real LLM output by setting ANTHROPIC_API_KEY]*"

	case strings.Contains(lower, "summar") || strings.Contains(lower, "review"):
		return "## Summary\n\nThe workflow has been processed successfully. Key takeaways:\n\n" +
			"1. The task was broken into logical, sequential steps.\n" +
			"2. Each step built upon the output of the previous one.\n" +
			"3. The final result synthesizes all intermediate outputs into a coherent whole.\n\n" +
			"**Status:** Complete. All objectives addressed.\n\n" +
			"*[Mock summary — set ANTHROPIC_API_KEY for real responses]*"

	case strings.Contains(lower, "edit") || strings.Contains(lower, "polish") || strings.Contains(lower, "refine"):
		return "## Refined Output\n\nAfter careful editing and polishing:\n\n" +
			"The content has been reviewed for clarity, accuracy, and flow. " +
			"Key improvements include better transitions between sections, " +
			"clearer language, and a stronger conclusion. " +
			"The final version is ready for delivery.\n\n" +
			"*[Mock edit — set ANTHROPIC_API_KEY for real responses]*"

	default:
		return fmt.Sprintf("## Step Output\n\nProcessed the following task:\n\n> %s\n\n"+
			"The step completed successfully. Output has been generated and is ready "+
			"to be passed to the next step in the workflow.\n\n"+
			"Key points addressed:\n"+
			"- Primary objective identified and handled\n"+
			"- Secondary considerations noted\n"+
			"- Output formatted for downstream consumption\n\n"+
			"*[Mock response — set ANTHROPIC_API_KEY for real LLM output]*",
			truncate(userPrompt, 100))
	}
}

// mockDecomposition returns a hardcoded JSON decomposition that the orchestrator can parse.
func (m *MockClient) mockDecomposition(userPrompt string) string {
	// Extract a short task label from the prompt for step names.
	task := truncate(userPrompt, 40)

	return fmt.Sprintf(`{
  "steps": [
    {
      "name": "research",
      "prompt": "Research and gather key information about: %s",
      "system_prompt": "You are a thorough research assistant. Provide detailed, factual information."
    },
    {
      "name": "analyze",
      "prompt": "Analyze the research findings and identify the most important points. Structure them clearly.",
      "system_prompt": "You are an analytical assistant. Identify patterns and key insights."
    },
    {
      "name": "draft",
      "prompt": "Using the analysis, write a comprehensive draft response that addresses the original task.",
      "system_prompt": "You are a skilled writer. Produce clear, well-structured content."
    },
    {
      "name": "review_and_finalize",
      "prompt": "Review the draft, improve clarity and completeness, then produce the final polished output.",
      "system_prompt": "You are an editor. Refine the content for quality and completeness."
    }
  ]
}`, task)
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}