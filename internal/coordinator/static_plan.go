package coordinator

// IntegrationCyclePlan returns a hardcoded plan for the three-agent
// integration cycle. Task 1 goes to the inference agent, task 2 to defi.
func IntegrationCyclePlan(inferenceAgentID, defiAgentID string) Plan {
	return Plan{
		FestivalID: "integration-cycle-001",
		Sequences: []PlanSequence{
			{
				ID: "seq-01",
				Tasks: []PlanTask{
					{
						ID:            "task-inference-01",
						Name:          "market_sentiment_analysis",
						AssignTo:      inferenceAgentID,
						ModelID:       "qwen/qwen-2.5-7b-instruct",
						Input:         "Analyze market sentiment for ETH",
						Priority:      1,
						MaxTokens:     512,
						PaymentAmount: 100,
					},
					{
						ID:            "task-defi-01",
						Name:          "execute_trade",
						TaskType:      "execute_trade",
						AssignTo:      defiAgentID,
						Priority:      1,
						PaymentAmount: 100,
						Dependencies:  []string{"task-inference-01"},
					},
				},
			},
		},
	}
}
