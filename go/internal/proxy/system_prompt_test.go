package proxy

import (
	"strings"
	"testing"
)

func TestBuildEnvironmentSection_Provider(t *testing.T) {
	// 1. zai provider
	secZai := BuildEnvironmentSection("glm-5.3", "zai")
	expectedZai := "- You are powered by the model named zai-api/glm-5.3."
	if !strings.Contains(secZai, expectedZai) {
		t.Errorf("BuildEnvironmentSection(glm-5.3, zai) should contain %q, got:\n%s", expectedZai, secZai)
	}

	// 2. bigmodel provider
	secBig := BuildEnvironmentSection("glm-4.6", "bigmodel")
	expectedBig := "- You are powered by the model named bigmodel-api/glm-4.6."
	if !strings.Contains(secBig, expectedBig) {
		t.Errorf("BuildEnvironmentSection(glm-4.6, bigmodel) should contain %q, got:\n%s", expectedBig, secBig)
	}

	// 3. custom or already suffixed provider
	secCustom := BuildEnvironmentSection("custom-model", "custom-api")
	expectedCustom := "- You are powered by the model named custom-api/custom-model."
	if !strings.Contains(secCustom, expectedCustom) {
		t.Errorf("BuildEnvironmentSection(custom-model, custom-api) should contain %q, got:\n%s", expectedCustom, secCustom)
	}

	// 4. empty provider should omit powered by line
	secEmpty := BuildEnvironmentSection("glm-5.3", "")
	if strings.Contains(secEmpty, "You are powered by the model named") {
		t.Errorf("BuildEnvironmentSection with empty provider should omit poweredByLine, got:\n%s", secEmpty)
	}
}

func TestTransformAnthropicBody_Provider(t *testing.T) {
	input := []byte(`{"model":"glm-5.3","messages":[{"role":"user","content":"hello"}]}`)
	transformed := TransformAnthropicBody(input, "test-user-id", true, "glm-5.3", "zai")

	str := string(transformed)
	if !strings.Contains(str, "zai-api/glm-5.3") {
		t.Errorf("transformed body missing zai-api/glm-5.3, got:\n%s", str)
	}
	if !strings.Contains(str, "test-user-id") {
		t.Errorf("transformed body missing test-user-id, got:\n%s", str)
	}
}
