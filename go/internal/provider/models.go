package provider

type ModelDef struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ContextWindow   int    `json:"context_window"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	Reasoning       bool   `json:"reasoning,omitempty"`
}

var Models = []ModelDef{
	{ID: "glm-4.5-air", Name: "GLM 4.5 Air", ContextWindow: 131072, MaxOutputTokens: 98304, Reasoning: true},
	{ID: "glm-4.6", Name: "GLM 4.6", ContextWindow: 200000, MaxOutputTokens: 131072, Reasoning: true},
	{ID: "glm-4.6v", Name: "GLM 4.6V", ContextWindow: 131072, MaxOutputTokens: 32768},
	{ID: "glm-4.7", Name: "GLM 4.7", ContextWindow: 200000, MaxOutputTokens: 131072, Reasoning: true},
	{ID: "glm-5", Name: "GLM 5", ContextWindow: 200000, MaxOutputTokens: 64000, Reasoning: true},
	{ID: "glm-5-turbo", Name: "GLM 5 Turbo", ContextWindow: 200000, MaxOutputTokens: 64000, Reasoning: true},
	{ID: "glm-5v-turbo", Name: "GLM 5V Turbo", ContextWindow: 200000, MaxOutputTokens: 131072},
	{ID: "glm-5.1", Name: "GLM 5.1", ContextWindow: 200000, MaxOutputTokens: 64000, Reasoning: true},
	{ID: "glm-5.2", Name: "GLM 5.2", ContextWindow: 1000000, MaxOutputTokens: 128000, Reasoning: true},
	{ID: "glm-5.3", Name: "GLM 5.3", ContextWindow: 1000000, MaxOutputTokens: 128000, Reasoning: true},
	{ID: "glm-5.3-flash", Name: "GLM 5.3 Flash", ContextWindow: 1000000, MaxOutputTokens: 128000, Reasoning: true},
}

func FindModel(id string) *ModelDef {
	for i := range Models {
		if Models[i].ID == id {
			return &Models[i]
		}
	}
	return nil
}

func IsGlm53Model(model string) bool {
	return model == "glm-5.3" || model == "glm-5.3-flash"
}

func IsReasoningModel(model string) bool {
	m := FindModel(model)
	return m != nil && m.Reasoning
}
